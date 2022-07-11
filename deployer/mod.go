package deployer

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/nkcr/hodor/config"
	"github.com/rs/xid"
	"github.com/rs/zerolog"
	"github.com/tidwall/buntdb"
)

// defaultSerde is the default serialization/de-serialization mechanism used
var defaultSerde = JSONSerde{}

// jobSize is the channel size used to store jobs
const jobSize = 50

// HTTPClient defines the function we expect from an HTTP client
type HTTPClient interface {
	Get(url string) (resp *http.Response, err error)
}

// Serde ddefines the primitives to marshal/unmarshal an element
type Serde interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSONSerde defines a serde type based on JSON
//
// - implements deployer.serde
type JSONSerde struct{}

// Marshal implements deployer.serde
func (JSONSerde) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal implements deployer.Serde
func (JSONSerde) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// JobStatus represents the status of a job. A job is created each time a
// deployment is triggered. It allows for asynchronous release deployment.
type JobStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Deployer defines the primitive needed to deploy releases
type Deployer interface {
	// Start must be called only once to start the job processing
	Start()
	// Stop must be called only once and when start has been called
	Stop()
	// Deploy triggers a job to deploy a release. It returns a jobID that can be
	// used to check the job's status.
	Deploy(releaseID string, releaseURL *url.URL) (string, error)
	// GetStatus returns the status of a job
	GetStatus(jobID string) (JobStatus, error)
}

// newJob returns a new initialized job
func newJob(releaseID string, releaseURL *url.URL) job {
	return job{
		id:         xid.New().String(),
		releaseID:  releaseID,
		releaseURL: releaseURL,
	}
}

// job is created each time a release is triggered. It contains information to
// download and deploy a release.
type job struct {
	id         string
	releaseID  string
	releaseURL *url.URL
}

// NewFileDeployer returns a new initialized file deployer
func NewFileDeployer(db *buntdb.DB, conf config.Config, client HTTPClient,
	logger zerolog.Logger) Deployer {

	logger = logger.With().Str("role", "deployer").Logger()

	return &FileDeployer{
		db:     db,
		config: conf,
		client: client,
		serde:  defaultSerde,
		logger: logger,
	}
}

// FileDeployer implements a Deployer that deploys releases on disk.
//
// - implements deployer.Deployer
type FileDeployer struct {
	sync.Mutex
	db     *buntdb.DB
	config config.Config
	jobs   chan job
	stop   bool
	client HTTPClient
	logger zerolog.Logger
	serde  Serde
}

// Start implements deployer.Deployer. This is a blocking function that handles
// jobs. It must be called only once.
func (fd *FileDeployer) Start() {
	fd.Lock()
	fd.jobs = make(chan job, jobSize)
	fd.stop = false
	fd.Unlock()

	fd.processJobs()
}

// processJobs loops over jobs and processes it
func (fd *FileDeployer) processJobs() {
	// This loop exits if the job chan is closed or the stop flag is true.
	for job := range fd.jobs {
		if fd.getStop() {
			return
		}

		err := fd.handleJob(job)
		if err != nil {
			err2 := fd.saveJobStatus(job.id, "failed", err.Error())
			if err2 != nil {
				fd.logger.Err(err2).Msgf("job failed: failed to save status. Error was: %v", err)
			}
			continue
		}

		err = fd.saveJobStatus(job.id, "ok", "job done")
		if err != nil {
			fd.logger.Err(err).Msg("job ok: failed to save status")
		}
	}
}

// saveJobStatus save the status of job onto the database
func (fd *FileDeployer) saveJobStatus(jobID, status, message string) error {
	jobStatus := JobStatus{
		Status:  status,
		Message: message,
	}

	buf, err := fd.serde.Marshal(&jobStatus)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %v", err)
	}

	err = fd.db.Update(func(tx *buntdb.Tx) error {
		_, _, err := tx.Set(jobID, string(buf), nil)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to save status: %v", err)
	}

	return nil
}

// Stop implements deployer.Deployer. Must be called only once and if already
// started.
func (fd *FileDeployer) Stop() {
	close(fd.jobs)
	fd.Lock()
	fd.stop = true
	fd.Unlock()
}

// getStop safely returns the stop status of the deployer. If true it means that
// the Stop() function has been called and therefore the deployer must be
// stopped.
func (fd *FileDeployer) getStop() bool {
	fd.Lock()
	defer fd.Unlock()
	return fd.stop
}

// Deploy implements deployer.Deployer. It adds a new job to the queue.
func (fd *FileDeployer) Deploy(releaseID string, releaseURL *url.URL) (string, error) {
	fd.logger.Info().Msgf("deploying release %q from %q", releaseID, releaseURL)

	if fd.getStop() {
		return "", errors.New("deployer is stopped")
	}

	job := newJob(releaseID, releaseURL)

	err := fd.saveJobStatus(job.id, "created", "job has been created")
	if err != nil {
		return "", fmt.Errorf("failed to set job status: %v", err)
	}

	select {
	case fd.jobs <- job:
		return job.id, nil
	default:
		return "", errors.New("buffer is full, re-try later")
	}
}

// GetStatus implements deployer.Deployer
func (fd *FileDeployer) GetStatus(key string) (JobStatus, error) {
	var jobStatus JobStatus
	var statusBuf string
	var err error

	err = fd.db.View(func(tx *buntdb.Tx) error {
		statusBuf, err = tx.Get(key, false)
		return err
	})

	if err == buntdb.ErrNotFound {
		return jobStatus, fmt.Errorf("key %q not found", key)
	}

	if err != nil {
		return jobStatus, fmt.Errorf("failed to get status: %v", err)
	}

	err = fd.serde.Unmarshal([]byte(statusBuf), &jobStatus)
	if err != nil {
		return jobStatus, fmt.Errorf("failed to unmarshal job status: %v", err)
	}

	return jobStatus, nil
}

// handleJob is called by the queue processor and processes a job. It downloads,
// extracts, and deploys a release.
func (fd *FileDeployer) handleJob(job job) error {
	fd.logger.Info().Msgf("starting job %q (release %q)", job.id, job.releaseID)

	targetFolder, found := fd.config.Entries[job.releaseID]
	if !found {
		return fmt.Errorf("releaseID %q not found from the config", job.releaseID)
	}

	res, err := fd.client.Get(job.releaseURL.String())
	if err != nil {
		return fmt.Errorf("failed to get file: %v", err)
	}

	tmpDest, err := ioutil.TempDir("", "hodor")
	if err != nil {
		return fmt.Errorf("failed to create tmp dir: %v", err)
	}

	fd.logger.Info().Msgf("job %q using temp folder %q (release %q)", job.id,
		tmpDest, job.releaseID)

	defer os.RemoveAll(tmpDest)

	tarRootFolder, err := saveTar(res.Body, tmpDest)
	if err != nil {
		return fmt.Errorf("failed to save tar file: %v", err)
	}

	// remove the actual target and move the extracted contents to the actual
	// target.

	os.RemoveAll(targetFolder)

	err = os.Rename(filepath.Join(tmpDest, tarRootFolder), targetFolder)
	if err != nil {
		return fmt.Errorf("failed to rename folder: %v", err)
	}

	fd.logger.Info().Msgf("job %q done (release %q)", job.id, job.releaseID)

	return nil
}

// saveTar extract a .tar.gz to the provided destination. It expects the tar.gz
// to be a folder.
func saveTar(r io.Reader, dest string) (string, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("failed to create reader: %v", err)
	}

	defer gzr.Close()

	tr := tar.NewReader(gzr)

	header, err := tr.Next()
	if err != nil {
		return "", fmt.Errorf("failed to read the first header: %v", err)
	}

	if header.Typeflag != tar.TypeDir {
		return "", errors.New("tar must be a folder")
	}

	tarRootFolder := header.Name
	tmpRootTarget := filepath.Join(dest, tarRootFolder)

	err = os.MkdirAll(tmpRootTarget, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create root dir %s: %v", tmpRootTarget, err)
	}

	err = untar(dest, tr)
	if err != nil {
		return "", fmt.Errorf("failed to extract: %v", err)
	}

	return tarRootFolder, nil
}

// untar walks through the tar's content and extracts the elements
func untar(dest string, tr *tar.Reader) error {
	for {
		header, err := tr.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("failed to get next: %v", err)
		}

		target := filepath.Join(dest, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			_, err := os.Stat(target)
			if err != nil {
				err := os.MkdirAll(target, 0755)
				if err != nil {
					return fmt.Errorf("failed to create dir %s: %v", target, err)
				}
			}

		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, 0755)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %v", target, err)
			}

			_, err = io.Copy(f, tr)
			if err != nil {
				return fmt.Errorf("failed to copy file %s: %v", target, err)
			}

			f.Close()
		}
	}

	return nil
}
