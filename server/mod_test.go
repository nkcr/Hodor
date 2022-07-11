package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/nkcr/hodor/deployer"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// This test performs a simple scenario. It starts the server and makes an HTTP
// request. The process should not return any error.
func TestScenario(t *testing.T) {
	logger := zerolog.New(io.Discard)

	deployer := fakeDeployer{
		deployReturn: "XX",
	}

	server := NewHookHTTP("localhost:0", deployer, logger)

	wait := sync.WaitGroup{}
	wait.Add(1)
	go func() {
		defer wait.Done()
		err := server.Start()
		require.NoError(t, err)
	}()

	defer func() {
		t.Log("stopping")
		server.Stop()
		wait.Wait()
		t.Log("stopped")
	}()

	time.Sleep(time.Second * 1)

	addr := server.GetAddr()
	require.NotNil(t, addr)

	reqURL, err := url.Parse("http://" + addr.String() + "/api/hook/YY")
	require.NoError(t, err)

	t.Logf("fetching url %s", reqURL)

	req := request{
		BrowserDownloadURL: "http://xx",
	}

	reqBuf, err := json.Marshal(&req)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(&http.Request{
		Method: http.MethodPost,
		Body:   io.NopCloser(bytes.NewBuffer(reqBuf)),
		URL:    reqURL,
	})
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)

	res, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, fmt.Sprintf("{\"jobID\":%q}", deployer.deployReturn), string(res))
}

func TestWrongAddr(t *testing.T) {
	a := HookHTTP{
		server: &http.Server{Addr: "x"},
	}

	err := a.Start()
	require.EqualError(t, err, "failed to create conn 'x': listen tcp: address x: missing port in address")
}

// If the listener is nil, the server should return a nil address.
func TestGetAddr(t *testing.T) {
	a := HookHTTP{}

	addr := a.GetAddr()
	require.Nil(t, addr)
}

func TestGetHookHandler_Wrong_Action(t *testing.T) {
	deployer := fakeDeployer{}

	handler := getHookHandler(deployer)

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "", nil)
	require.NoError(t, err)

	handler(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Result().StatusCode)

	buff, err := ioutil.ReadAll(rr.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "wrong action\n", string(buff))
}

func TestGetHookHandler_Wrong_Request(t *testing.T) {
	deployer := fakeDeployer{}

	handler := getHookHandler(deployer)

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodPost, "", new(bytes.Buffer))
	require.NoError(t, err)

	handler(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Result().StatusCode)

	buff, err := ioutil.ReadAll(rr.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "failed to decode request: EOF\n", string(buff))
}

func TestGetHookHandler_Wrong_URL(t *testing.T) {
	deployer := fakeDeployer{}

	handler := getHookHandler(deployer)

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodPost, "", bytes.NewBufferString("{}"))
	require.NoError(t, err)

	handler(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Result().StatusCode)

	buff, err := ioutil.ReadAll(rr.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "wrong url: parse \"\": empty url\n", string(buff))
}

func TestGetHookHandler_Deployer_Fail(t *testing.T) {
	deployer := fakeDeployer{
		deployeErr: errors.New("fake"),
	}

	handler := getHookHandler(deployer)
	body := bytes.NewBufferString("{\"browser_download_url\":\"http://xx\"}")

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodPost, "", body)
	require.NoError(t, err)

	handler(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Result().StatusCode)

	buff, err := ioutil.ReadAll(rr.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "failed to deploy: fake\n", string(buff))
}

func TestGetStatusHandler_Wrong_Action(t *testing.T) {
	deployer := fakeDeployer{}

	handler := getStatusHandler(deployer)

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodPost, "", nil)
	require.NoError(t, err)

	handler(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Result().StatusCode)

	buff, err := ioutil.ReadAll(rr.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "wrong action\n", string(buff))
}

func TestGetStatusHandler_Deployer_Fail(t *testing.T) {
	deployer := fakeDeployer{
		statusErr: errors.New("fake"),
	}

	handler := getStatusHandler(deployer)

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "", nil)
	require.NoError(t, err)

	handler(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Result().StatusCode)

	buff, err := ioutil.ReadAll(rr.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "failed to get status: fake\n", string(buff))
}

func TestGetStatusHandler_Pass(t *testing.T) {
	deployer := fakeDeployer{
		status: deployer.JobStatus{Status: "XX"},
	}

	handler := getStatusHandler(deployer)

	rr := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "", nil)
	require.NoError(t, err)

	handler(rr, req)

	require.Equal(t, http.StatusOK, rr.Result().StatusCode)

	buff, err := ioutil.ReadAll(rr.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "{\"status\":\"XX\",\"message\":\"\"}\n", string(buff))
}

// ----------------------------------------------------------------------------
// Utility function

type fakeDeployer struct {
	deployer.Deployer

	deployReturn string
	deployeErr   error

	status    deployer.JobStatus
	statusErr error
}

func (d fakeDeployer) Deploy(releaseID string, releaseURL *url.URL) (string, error) {
	return d.deployReturn, d.deployeErr
}

func (d fakeDeployer) GetStatus(jobID string) (deployer.JobStatus, error) {
	return d.status, d.statusErr
}
