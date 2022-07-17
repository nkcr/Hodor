package deployer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nkcr/hodor/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/buntdb"
)

func TestDeployer_Scenario_Pass(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	releaseID := "XX"
	tag := "YY"

	tmpDir, err := ioutil.TempDir("", "hodortest")
	require.NoError(t, err)

	releaseGz, releaseContent := createTar(t, tmpDir)

	t.Logf("using temp folder %q", tmpDir)
	defer os.RemoveAll(tmpDir)

	target := filepath.Join(tmpDir, "target")

	conf := config.Config{
		Entries: map[string]string{
			releaseID: target,
		},
	}
	client := fakeClient{
		body: releaseGz,
	}
	logger := zerolog.New(io.Discard)

	deployer := NewFileDeployer(db, conf, client, logger)

	wait := sync.WaitGroup{}
	wait.Add(1)
	go func() {
		defer wait.Done()
		deployer.Start()
	}()

	defer func() {
		t.Log("stopping")
		deployer.Stop()
		wait.Wait()
		t.Log("stopped")
	}()

	time.Sleep(time.Second)

	jobID, err := deployer.Deploy(releaseID, tag, &url.URL{})
	require.NoError(t, err)

	time.Sleep(time.Second)

	status, err := deployer.GetStatus(jobID)
	require.NoError(t, err)

	require.Equal(t, "ok", status.Status)
	require.Equal(t, "job done", status.Message)

	latestTag, err := deployer.GetLatestTag(releaseID)

	require.NoError(t, err)
	require.Equal(t, tag, latestTag)

	fileInfos, err := ioutil.ReadDir(target)
	require.NoError(t, err)
	require.Len(t, fileInfos, 2)

	buf, err := os.ReadFile(filepath.Join(target, "el.txt"))
	require.NoError(t, err)
	require.Equal(t, releaseContent, string(buf))
}

func TestProcessJobs_Stop(t *testing.T) {
	jobs := make(chan job, 2)
	jobs <- job{}
	jobs <- job{}

	fd := FileDeployer{
		stop: true,
		jobs: jobs,
	}

	fd.processJobs()

	// only one jobs should be processed
	require.Len(t, jobs, 1)
}

func TestProcessJobs_Handle_Fail(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	jobs := make(chan job, 1)
	jobID := "XX"
	releaseID := "YY"
	jobs <- job{
		id:        jobID,
		releaseID: releaseID,
	}
	close(jobs)

	fd := FileDeployer{
		stop:  false,
		jobs:  jobs,
		db:    db,
		serde: defaultSerde,
	}

	fd.processJobs()

	status, err := fd.GetStatus(jobID)
	require.NoError(t, err)
	require.Equal(t, "failed", status.Status)
	require.Equal(t, fmt.Sprintf("releaseID %q not found from the config", releaseID), status.Message)
}

func TestProcessJobs_Handle_Fail_Status_Fail(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	jobs := make(chan job, 1)
	jobID := "XX"
	releaseID := "YY"
	jobs <- job{
		id:        jobID,
		releaseID: releaseID,
	}
	close(jobs)

	log := new(bytes.Buffer)
	logger := zerolog.New(log)

	fd := FileDeployer{
		stop:   false,
		jobs:   jobs,
		db:     db,
		serde:  fakeSerde{errors.New("fakes")},
		logger: logger,
	}

	fd.processJobs()

	_, err = fd.GetStatus(jobID)
	require.EqualError(t, err, fmt.Sprintf("key %q not found", "XX"))

	require.Contains(t, log.String(), "job failed: failed to save status")
}

func TestProcessJobs_Handle_Pass_Status_Fail(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	tmpDir, err := ioutil.TempDir("", "hodortest")
	require.NoError(t, err)

	releaseGz, _ := createTar(t, tmpDir)

	t.Logf("using temp folder %q", tmpDir)
	defer os.RemoveAll(tmpDir)

	jobs := make(chan job, 1)
	jobID := "XX"
	releaseID := "YY"
	jobs <- job{
		id:         jobID,
		releaseID:  releaseID,
		releaseURL: &url.URL{},
	}
	close(jobs)

	log := new(bytes.Buffer)
	logger := zerolog.New(log)

	fd := FileDeployer{
		stop:   false,
		jobs:   jobs,
		db:     db,
		serde:  fakeSerde{errors.New("fakes")},
		logger: logger,
		client: fakeClient{body: releaseGz},
		config: config.Config{
			Entries: map[string]string{
				releaseID: filepath.Join(tmpDir, "YY"),
			},
		},
	}

	fd.processJobs()

	require.Contains(t, log.String(), "job ok: failed to save status")
}

func TestDeploy_Not_Started(t *testing.T) {
	fd := FileDeployer{
		stop: true,
	}

	_, err := fd.Deploy("", "", nil)
	require.EqualError(t, err, "deployer is stopped")
}

func TestDeploy_Update_Status_Fail(t *testing.T) {
	fd := FileDeployer{
		serde: fakeSerde{err: errors.New("fake")},
	}

	_, err := fd.Deploy("", "", nil)
	require.EqualError(t, err, "failed to set job status: failed to marshal status: fake")
}

func TestDeploy_Update_Buffer_Full(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	fd := FileDeployer{
		serde: fakeSerde{},
		db:    db,
		jobs:  make(chan job),
	}

	_, err = fd.Deploy("", "", nil)
	require.EqualError(t, err, "buffer is full, re-try later")
}

func TestGetStatus_Key_Not_Found(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	fd := FileDeployer{
		db: db,
	}

	key := "XX"

	_, err = fd.GetStatus(key)
	require.EqualError(t, err, fmt.Sprintf("key %q not found", key))
}

func TestGetStatus_Unmarshal_Fail(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	key := "XX"

	err = db.Update(func(tx *buntdb.Tx) error {
		_, _, err = tx.Set(key, "", nil)
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	fd := FileDeployer{
		db:    db,
		serde: fakeSerde{err: errors.New("fake")},
	}

	_, err = fd.GetStatus(key)
	require.EqualError(t, err, "failed to unmarshal job status: fake")
}

func TestGetLatestTag_Not_Found(t *testing.T) {
	db, err := buntdb.Open(":memory:")
	require.NoError(t, err)

	fd := FileDeployer{
		db: db,
	}

	tag, err := fd.GetLatestTag("XX")

	require.NoError(t, err)
	require.Equal(t, "unknown", tag)
}

func TestHandleJob_Release_Not_Found(t *testing.T) {
	releaseID := "XX"

	conf := config.Config{
		Entries: map[string]string{},
	}

	fd := FileDeployer{
		config: conf,
	}

	job := job{
		id:         "",
		releaseID:  releaseID,
		releaseURL: &url.URL{},
	}

	err := fd.handleJob(job)
	require.EqualError(t, err, fmt.Sprintf("releaseID %q not found from the config", releaseID))
}

func TestHandleJob_Release_GET_Failed(t *testing.T) {
	releaseID := "XX"

	client := fakeClient{
		err: errors.New("fake"),
	}

	conf := config.Config{
		Entries: map[string]string{
			releaseID: "YY",
		},
	}

	fd := FileDeployer{
		config: conf,
		client: client,
	}

	job := job{
		id:         "",
		releaseID:  releaseID,
		releaseURL: &url.URL{},
	}

	err := fd.handleJob(job)
	require.EqualError(t, err, "failed to get file: fake")
}

func TestHandleJob_Untar_Failed(t *testing.T) {
	releaseID := "XX"

	client := fakeClient{
		body: &bytes.Buffer{},
	}

	conf := config.Config{
		Entries: map[string]string{
			releaseID: "YY",
		},
	}

	fd := FileDeployer{
		config: conf,
		client: client,
	}

	job := job{
		id:         "",
		releaseID:  releaseID,
		releaseURL: &url.URL{},
	}

	err := fd.handleJob(job)
	require.EqualError(t, err, "failed to save tar file: failed to create reader: EOF")
}

func TestSaveTar_Pass(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "hodortest")
	require.NoError(t, err)

	releaseGz, releaseContent := createTar(t, tmpDir)

	t.Logf("using temp folder %q", tmpDir)
	defer os.RemoveAll(tmpDir)

	target := filepath.Join(tmpDir, "target")

	rootTar, err := saveTar(releaseGz, target)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tmpDir, "release"), rootTar)

	fileInfos, err := ioutil.ReadDir(filepath.Join(target, rootTar))
	require.NoError(t, err)
	require.Len(t, fileInfos, 2)

	buf, err := os.ReadFile(filepath.Join(target, rootTar, "el.txt"))
	require.NoError(t, err)
	require.Equal(t, releaseContent, string(buf))
}

func TestSaveTar_Not_Folder(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "hodortest")
	require.NoError(t, err)

	t.Logf("using temp folder %q", tmpDir)
	defer os.RemoveAll(tmpDir)

	target := filepath.Join(tmpDir, "target")
	releaseEl := filepath.Join(tmpDir, "release.txt")
	releaseContent := "ZZ"

	f, err := os.Create(releaseEl)
	require.NoError(t, err)

	f.WriteString(releaseContent)
	f.Close()

	releaseGz := new(bytes.Buffer)

	err = compress(releaseEl, releaseGz)
	require.NoError(t, err)

	_, err = saveTar(releaseGz, target)
	require.EqualError(t, err, "tar must be a folder")
}

// ----------------------------------------------------------------------------
// Utility functions

type fakeClient struct {
	body io.Reader
	err  error
}

func (c fakeClient) Get(url string) (resp *http.Response, err error) {
	body := io.NopCloser(c.body)

	return &http.Response{
		Body: body,
	}, c.err
}

type fakeSerde struct {
	err error
}

func (s fakeSerde) Marshal(v any) ([]byte, error) {
	return nil, s.err
}

func (s fakeSerde) Unmarshal(data []byte, v any) error {
	return s.err
}

func createTar(t *testing.T, folder string) (*bytes.Buffer, string) {
	release := filepath.Join(folder, "release")
	releaseEl := filepath.Join(release, "el.txt")
	releaseContent := "ZZ"
	releaseSubFolder := filepath.Join(release, "sub")

	err := os.MkdirAll(releaseSubFolder, 0755)
	require.NoError(t, err)

	f, err := os.Create(releaseEl)
	require.NoError(t, err)

	f.WriteString(releaseContent)
	f.Close()

	releaseGz := new(bytes.Buffer)

	err = compress(release, releaseGz)
	require.NoError(t, err)

	return releaseGz, releaseContent
}

// https://gist.github.com/mimoo/25fc9716e0f1353791f5908f94d6e726
func compress(src string, buf io.Writer) error {
	// tar > gzip > buf
	zr := gzip.NewWriter(buf)
	tw := tar.NewWriter(zr)

	// walk through every file in the folder
	filepath.Walk(src, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// generate tar header
		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		// must provide real name
		// (see https://golang.org/src/archive/tar/common.go?#L626)
		header.Name = filepath.ToSlash(file)

		// write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		// if not a dir, write file content
		if !fi.IsDir() {
			data, err := os.Open(file)
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, data); err != nil {
				return err
			}
		}
		return nil
	})

	// produce tar
	if err := tw.Close(); err != nil {
		return err
	}
	// produce gzip
	if err := zr.Close(); err != nil {
		return err
	}

	return nil
}
