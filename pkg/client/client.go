package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/httputil"
)

const (
	namespacesURL = "namespaces"
	clustersURL   = "clusters"
	accessURL     = "access"
	kubeconfigURL = "kubeconfig"
)

type Config struct {
	Username string
	Password string
}

type Client struct {
	url        *url.URL
	config     *Config
	httpClient *http.Client
}

func NewClient(url *url.URL, config *Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = httputil.DefaultClient
	}
	return &Client{
		url:        url,
		config:     config,
		httpClient: httpClient,
	}
}

func (c *Client) get(ctx context.Context, out interface{}, s ...string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", getURL(c.url, s...), nil)
	if err != nil {
		return err
	}

	return c.performRequest(req, out)
}

func (c *Client) delete(ctx context.Context, s ...string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", getURL(c.url, s...), nil)
	if err != nil {
		return err
	}

	var noop interface{}
	return c.performRequest(req, &noop)
}

func (c *Client) post(ctx context.Context, in, out interface{}, s ...string) error {
	var reqBytes []byte

	switch v := in.(type) {
	case string:
		reqBytes = []byte(v)
	default:
		var err error
		reqBytes, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}

	reader := bytes.NewReader(reqBytes)

	req, err := http.NewRequestWithContext(ctx, "POST", getURL(c.url, s...), reader)
	if err != nil {
		return err
	}

	return c.performRequest(req, out)
}

func (c *Client) performRequest(req *http.Request, out interface{}) error {
	req.Header.Add(models.ClientClientRequestID, uuid.New().String())
	if c.config.Username != "" && c.config.Password != "" {
		req.SetBasicAuth(c.config.Username, c.config.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return c.handleResponse(resp, out)
}

func (c *Client) handleResponse(resp *http.Response, out interface{}) error {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		switch o := out.(type) {
		case *string:
			bytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			*o = string(bytes)
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(&out)
	default:
		bytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf(string(bytes))
	}
}

func getURL(u *url.URL, s ...string) string {
	return strings.Join(append([]string{u.String()}, s...), "/")
}
