package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
)

type Client struct {
	url        *url.URL
	httpClient *httputil.Client
}

func NewClient(url *url.URL, httpClient *httputil.Client) *Client {
	if httpClient == nil {
		httpClient = httputil.DefaultClient
	}
	return &Client{
		url:        url,
		httpClient: httpClient,
	}
}

func (c *Client) get(ctx context.Context, out interface{}, s ...string) error {
	req, err := httputil.NewCliRequest(ctx, "GET", getURL(c.url, s...), nil)
	if err != nil {
		return err
	}

	return c.performRequest(req, out)
}

func (c *Client) delete(ctx context.Context, s ...string) error {
	req, err := httputil.NewCliRequest(ctx, "DELETE", getURL(c.url, s...), nil)
	if err != nil {
		return err
	}

	var noop interface{}
	return c.performRequest(req, &noop)
}

func (c *Client) patch(ctx context.Context, in, out interface{}, s ...string) error {
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

	req, err := httputil.NewCliRequest(ctx, http.MethodPatch, getURL(c.url, s...), reader)
	if err != nil {
		return err
	}

	return c.performRequest(req, out)
}

func (c *Client) put(ctx context.Context, in, out interface{}, s ...string) error {
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

	req, err := httputil.NewCliRequest(ctx, http.MethodPut, getURL(c.url, s...), reader)
	if err != nil {
		return err
	}

	return c.performRequest(req, out)
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

	req, err := httputil.NewCliRequest(ctx, "POST", getURL(c.url, s...), reader)
	if err != nil {
		return err
	}

	return c.performRequest(req, out)
}

func (c *Client) performRequest(req *http.Request, out interface{}) error {
	req.Header.Add(models.ClientClientRequestID, uuid.New().String())

	resp, err := c.httpClient.Do(&httputil.Request{req})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return c.handleResponse(resp, out)
}

func (c *Client) handleResponse(resp *httputil.Response, out interface{}) error {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		switch o := out.(type) {
		case *string:
			bytes, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			*o = string(bytes)
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(&out)
	default:
		bytes, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf(string(bytes))
	}
}

func getURL(u *url.URL, s ...string) string {
	return strings.Join(append([]string{u.String()}, s...), "/")
}
