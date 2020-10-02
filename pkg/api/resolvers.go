package api

//go:generate go run github.com/99designs/gqlgen
import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dstotijn/hetty/pkg/reqlog"
	"github.com/google/uuid"
)

type Resolver struct {
	RequestLogService *reqlog.Service
}

type queryResolver struct{ *Resolver }

func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

func Filter(ss []reqlog.Request, test func(reqlog.Request) bool) (ret []reqlog.Request) {
	for _, s := range ss {
		if test(s) {
			ret = append(ret, s)
		}
	}
	return
}

func DerefString(s *string) string {
	if s != nil {
		return *s
	}

	return ""
}

func OpenBodyToRead(body io.ReadCloser) string {
	if body == nil {
		return ""
	}
	buf := new(strings.Builder)
	n, err := io.Copy(buf, body)
	// check errors
	if err != nil || n == 0 {
		return ""
	}
	return buf.String()
}

func (r *queryResolver) HTTPRequestLogs(ctx context.Context, filter string) ([]HTTPRequestLog, error) {
	reqs, err := r.RequestLogService.FindAllRequests(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("could not query repository for requests: %v", err)
	}

	logs := make([]HTTPRequestLog, len(reqs))

	for i, req := range reqs {
		req, err := parseRequestLog(req)
		if err != nil {
			return nil, err
		}
		logs[i] = req
	}

	return logs, nil
}

func (r *queryResolver) HTTPRequestLog(ctx context.Context, id string) (*HTTPRequestLog, error) {
	reqLogID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid id: %v", err)
	}
	log, err := r.RequestLogService.FindRequestLogByID(ctx, reqLogID)
	if err == reqlog.ErrRequestNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not get request by ID: %v", err)
	}
	req, err := parseRequestLog(log)
	if err != nil {
		return nil, err
	}

	return &req, nil
}

func parseRequestLog(req reqlog.Request) (HTTPRequestLog, error) {
	method := HTTPMethod(req.Request.Method)
	if !method.IsValid() {
		return HTTPRequestLog{}, fmt.Errorf("request has invalid method: %v", method)
	}

	log := HTTPRequestLog{
		ID:        req.ID.String(),
		URL:       req.Request.URL.String(),
		Proto:     req.Request.Proto,
		Method:    method,
		Timestamp: req.Timestamp,
	}

	if len(req.Body) > 0 {
		reqBody := string(req.Body)
		log.Body = &reqBody
	}

	if req.Request.Header != nil {
		log.Headers = make([]HTTPHeader, 0)
		for key, values := range req.Request.Header {
			for _, value := range values {
				log.Headers = append(log.Headers, HTTPHeader{
					Key:   key,
					Value: value,
				})
			}
		}
	}

	if req.Response != nil {
		log.Response = &HTTPResponseLog{
			RequestID:  req.ID.String(),
			Proto:      req.Response.Response.Proto,
			Status:     req.Response.Response.Status,
			StatusCode: req.Response.Response.StatusCode,
		}
		if len(req.Response.Body) > 0 {
			resBody := string(req.Response.Body)
			log.Response.Body = &resBody
		}
		if req.Response.Response.Header != nil {
			log.Response.Headers = make([]HTTPHeader, 0)
			for key, values := range req.Response.Response.Header {
				for _, value := range values {
					log.Response.Headers = append(log.Response.Headers, HTTPHeader{
						Key:   key,
						Value: value,
					})
				}
			}
		}
	}

	return log, nil
}
