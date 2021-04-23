package proxy_test

import (
	"bytes"
	"github.com/dstotijn/hetty/pkg/proxy"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
)

func getRequests() ([]proxy.RequestEntry, error) {
	return []proxy.RequestEntry{
		{
			MessageEntry: proxy.MessageEntry{
				UrlEquals: "https://example.com",
				Headers: map[string]string{
					"Authorization": "Bearer 12345",
				},
			},
		},
		{
			MessageEntry: proxy.MessageEntry{
				Method:      "POST",
				UrlEndsWith: "https://example.com/user",
				Body:        "{ \"name\": \"JavaScript\" }",
			},
		}, {
			MessageEntry: proxy.MessageEntry{
				Method:      "UPDATE",
				UrlEndsWith: "https://example.com/user",
				Body:        "{ \"name\": \"TypeScript\" }",
			},
		}}, nil
}

func getResponses() ([]proxy.ResponseEntry, error) {
	return []proxy.ResponseEntry{
		{
			MessageEntry: proxy.MessageEntry{
				UrlEquals: "https://example.com",
				Headers: map[string]string{
					"X-Proxy": "Hetty",
				},
			},
		},
		{
			MessageEntry: proxy.MessageEntry{
				UrlEndsWith: "/user",
				Method:      "GET",
				Body:        "{ \"name\": \"Hetty\" }",
			},
		},
		{
			MessageEntry: proxy.MessageEntry{
				UrlEndsWith: "/user",
				Method:      "POST",
				Body:        "{ \"name\": \"Golang\" }",
			},
		},
		{
			MessageEntry: proxy.MessageEntry{
				UrlStartsWith: "https://example.com/blog",
			},
			StatusCode: 404,
		},
	}, nil
}

func newResponse(req *http.Request, body string) http.Response {
	return http.Response{
		Request: req,
		Header:  http.Header{},
		Body:    ioutil.NopCloser(bytes.NewBuffer([]byte(body))),
	}
}

func newIntercept() (*proxy.Intercept, proxy.RequestModifyFunc, proxy.ResponseModifyFunc) {
	intercept, _ := proxy.NewIntercept(getRequests, getResponses)
	nextReq := func(req *http.Request) {}
	nextRes := func(res *http.Response) error { return nil }
	return intercept, nextReq, nextRes
}

func TestRequestInterceptorModifyHeaders(t *testing.T) {
	intercept, next, _ := newIntercept()
	req, _ := http.NewRequest("POST", "https://example.com", strings.NewReader(""))

	intercept.RequestInterceptor(next)(req)

	assert.Equal(t, "Bearer 12345", req.Header.Get("Authorization"))
}

func TestRequestInterceptorModifyBodyPOST(t *testing.T) {
	intercept, next, _ := newIntercept()

	req, _ := http.NewRequest("POST", "https://example.com/user", strings.NewReader(""))

	intercept.RequestInterceptor(next)(req)

	body, _ := ioutil.ReadAll(req.Body)

	assert.Equal(t, "{ \"name\": \"JavaScript\" }", string(body))
}

func TestRequestInterceptorModifyBodyUPDATE(t *testing.T) {
	intercept, next, _ := newIntercept()

	req, _ := http.NewRequest("UPDATE", "https://example.com/user", strings.NewReader(""))

	intercept.RequestInterceptor(next)(req)

	body, _ := ioutil.ReadAll(req.Body)

	assert.Equal(t, "{ \"name\": \"TypeScript\" }", string(body))
}

func TestResponseInterceptorModifyHeaders(t *testing.T) {
	intercept, _, next := newIntercept()

	req, _ := http.NewRequest("GET", "https://example.com", strings.NewReader(""))

	res := newResponse(req, "")

	intercept.ResponseInterceptor(next)(&res)

	assert.Equal(t, "Hetty", res.Header.Get("X-Proxy"))
}

func TestResponseInterceptorModifyBodyGET(t *testing.T) {
	intercept, _, next := newIntercept()

	req, _ := http.NewRequest("GET", "https://example.com/user", strings.NewReader(""))

	res := newResponse(req, "{ \"name\": \"Burp\" }")
	intercept.ResponseInterceptor(next)(&res)

	body, _ := ioutil.ReadAll(res.Body)

	assert.Equal(t, "{ \"name\": \"Hetty\" }", string(body))
}

func TestResponseInterceptorModifyBodyPOST(t *testing.T) {
	intercept, _, next := newIntercept()

	req, _ := http.NewRequest("POST", "https://example.com/user", strings.NewReader(""))

	res := newResponse(req, "{ \"name\": \"Python\" }")
	intercept.ResponseInterceptor(next)(&res)

	body, _ := ioutil.ReadAll(res.Body)

	assert.Equal(t, "{ \"name\": \"Golang\" }", string(body))
}

func TestResponseInterceptorModifyStatus(t *testing.T) {
	intercept, _, next := newIntercept()

	req, _ := http.NewRequest("GET", "https://example.com/blog/2020", strings.NewReader(""))

	res := newResponse(req, "")
	intercept.ResponseInterceptor(next)(&res)

	assert.Equal(t, 404, res.StatusCode)
}
