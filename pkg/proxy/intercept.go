package proxy

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type HTTPMessage struct {
	Body   *io.ReadCloser
	Header http.Header
}

func changeBody(msg *HTTPMessage, modifer func(body []byte) []byte) error {
	body, err := ioutil.ReadAll(*msg.Body)
	if err != nil {
		return fmt.Errorf("Could not read response body: %v", err)
	}

	contentEncoding := msg.Header.Get("Content-Encoding")

	if contentEncoding == "" {
		newBody := modifer(body)
		*msg.Body = ioutil.NopCloser(bytes.NewBuffer(newBody))
	} else if contentEncoding == "gzip" {
		gzipReader, err := gzip.NewReader(bytes.NewBuffer(body))
		if err != nil {
			return fmt.Errorf("Could not create gzip reader: %v", err)
		}
		defer gzipReader.Close()
		body, err = ioutil.ReadAll(gzipReader)

		if err != nil {
			return fmt.Errorf("Could not read gzipped response body: %v", err)
		}

		newBody := modifer(body)
		var gzipBodyBuffer bytes.Buffer
		gzipWriter := gzip.NewWriter(&gzipBodyBuffer)
		if _, err := gzipWriter.Write(newBody); err != nil {
			return fmt.Errorf("Could not write gzip body: %v", err)
		}
		if err := gzipWriter.Close(); err != nil {
			return fmt.Errorf("Could not close gzip body writer: %v", err)
		}

		*msg.Body = ioutil.NopCloser(&gzipBodyBuffer)
		msg.Header.Set("Content-Length", strconv.Itoa(gzipBodyBuffer.Len()))

	} else if contentEncoding == "br" {
		// TODO: Handle brotli properly
		newBody := modifer(body)
		*msg.Body = ioutil.NopCloser(bytes.NewBuffer(newBody))
		msg.Header.Del("Content-Encoding")
	} else {
		return fmt.Errorf("Unknown encoding: %v", contentEncoding)
	}

	return nil
}

type MessageEntry struct {
	// Filters
	UrlEquals     string `yaml:"url_equals" mapstructure:"url_equals"`
	UrlStartsWith string `yaml:"url_starts_with" mapstructure:"url_starts_with"`
	UrlEndsWith   string `yaml:"url_ends_with" mapstructure:"url_ends_with"`
	Method        string

	// Modify
	Body    string
	Headers map[string]string `yaml:"headers,omitempty"`
}

type RequestEntry struct {
	MessageEntry `yaml:",inline" mapstructure:",squash"`
}

type ResponseEntry struct {
	MessageEntry `yaml:",inline" mapstructure:",squash"`
	StatusCode   int
}

type Intercept struct {
	getRequests  func() ([]RequestEntry, error)
	getResponses func() ([]ResponseEntry, error)
}

func NewIntercept(
	getRequests func() ([]RequestEntry, error),
	getResponses func() ([]ResponseEntry, error),
) (*Intercept, error) {
	err := provisionInterceptYaml()
	if err != nil {
		return nil, err
	}

	return &Intercept{
		getRequests:  getRequests,
		getResponses: getResponses,
	}, nil
}

type InterceptYamlFormat struct {
	Responses []ResponseEntry
	Requests  []RequestEntry
}

func getInterceptYamlPath() string {
	path, _ := filepath.Abs("./intercept.yaml")
	return path
}

func provisionInterceptYaml() error {
	path := getInterceptYamlPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = ioutil.WriteFile(path, []byte("responses:\n  - url_equals: https://example.com/\n    body: <h1>Hetty</h1>\n"), 0644)
		if err != nil {
			return fmt.Errorf("intercet: error while provisioning intercept.yaml")
		}
		log.Println("[INFO] Provisioned intercept.yaml. Try it by accessing https://example.com/")
		return nil
	}
	return nil
}

func getInterceptYaml() ([]ResponseEntry, []RequestEntry, error) {
	path := getInterceptYamlPath()
	fileContent, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("intercept: error reading intercept.yaml: %v", err)
	}
	var fileOutput interface{}
	err = yaml.Unmarshal(fileContent, &fileOutput)
	if err != nil {
		return nil, nil, fmt.Errorf("intercept: error unmarshaling intercept.yaml: %v", err)
	}
	var reqResConfig InterceptYamlFormat
	err = mapstructure.Decode(fileOutput, &reqResConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("intercept: decoding error: %v", err)
	}

	return reqResConfig.Responses, reqResConfig.Requests, nil
}

func GetRequestsFromYaml() ([]RequestEntry, error) {
	_, requests, err := getInterceptYaml()
	if err != nil {
		return nil, err
	}
	return requests, nil
}

func GetResponsesFromYaml() ([]ResponseEntry, error) {
	responses, _, err := getInterceptYaml()
	if err != nil {
		return nil, err
	}
	return responses, nil
}

func isMatchingUrl(entry MessageEntry, url string) bool {
	return url == entry.UrlEquals ||
		(entry.UrlStartsWith != "" && strings.HasPrefix(url, entry.UrlStartsWith)) ||
		(entry.UrlEndsWith != "" && strings.HasSuffix(url, entry.UrlEndsWith))
}

func isMatchingMethod(entry MessageEntry, method string) bool {
	return entry.Method == "" || entry.Method == method
}

func (intercept *Intercept) RequestInterceptor(next RequestModifyFunc) RequestModifyFunc {
	return func(req *http.Request) {
		next(req)

		requests, err := intercept.getRequests()
		if err != nil {
			log.Fatal(err)
		}

		for _, request := range requests {
			url := req.URL.String()

			if isMatchingUrl(request.MessageEntry, url) && isMatchingMethod(request.MessageEntry, req.Method) {
				if request.Body != "" {
					err := changeBody(&HTTPMessage{Header: req.Header, Body: &req.Body}, func(b []byte) []byte {
						return []byte(request.Body)
					})
					if err != nil {
						panic(err)
					}
				}

				if len(request.Headers) != 0 {
					for key, value := range request.Headers {
						req.Header.Set(key, value)
					}
				}
			}
		}
	}
}

func (intercept *Intercept) ResponseInterceptor(next ResponseModifyFunc) ResponseModifyFunc {
	return func(res *http.Response) error {
		if err := next(res); err != nil {
			return err
		}

		responses, err := intercept.getResponses()
		if err != nil {
			return err
		}

		for _, response := range responses {

			url := res.Request.URL.String()

			if isMatchingUrl(response.MessageEntry, url) && isMatchingMethod(response.MessageEntry, res.Request.Method) {
				if response.Body != "" {
					err := changeBody(&HTTPMessage{Header: res.Header, Body: &res.Body}, func(b []byte) []byte {
						return []byte(response.Body)
					})
					if err != nil {
						panic(err)
					}
				}

				if len(response.Headers) != 0 {
					for key, value := range response.Headers {
						res.Header.Set(key, value)
					}
				}

				if response.StatusCode != 0 {
					res.StatusCode = response.StatusCode
				}
			}
		}

		return nil
	}
}
