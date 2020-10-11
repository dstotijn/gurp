package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/dstotijn/hetty/pkg/reqlog"
	"github.com/dstotijn/hetty/pkg/scope"

	"github.com/99designs/gqlgen/graphql"
	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"

	// Register sqlite3 for use via database/sql.
	_ "github.com/mattn/go-sqlite3"
)

// Client implements reqlog.Repository.
type Client struct {
	db *sqlx.DB
}

type httpRequestLogsQuery struct {
	requestCols        []string
	requestHeaderCols  []string
	responseHeaderCols []string
	joinResponse       bool
}

// New returns a new Client.
func New(filename string) (*Client, error) {
	// Create directory for DB if it doesn't exist yet.
	if dbDir, _ := filepath.Split(filename); dbDir != "" {
		if _, err := os.Stat(dbDir); os.IsNotExist(err) {
			os.Mkdir(dbDir, 0755)
		}
	}

	opts := make(url.Values)
	opts.Set("_foreign_keys", "1")

	dsn := fmt.Sprintf("file:%v?%v", filename, opts.Encode())
	db, err := sqlx.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("sqlite: could not ping database: %v", err)
	}

	c := &Client{db: db}

	if err := c.prepareSchema(); err != nil {
		return nil, fmt.Errorf("sqlite: could not prepare schema: %v", err)
	}

	return &Client{db: db}, nil
}

func (c Client) prepareSchema() error {
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS http_requests (
		id INTEGER PRIMARY KEY,
		proto TEXT,
		url TEXT,
		method TEXT,
		body BLOB,
		timestamp DATETIME
	)`)
	if err != nil {
		return fmt.Errorf("could not create http_requests table: %v", err)
	}

	_, err = c.db.Exec(`CREATE TABLE IF NOT EXISTS http_responses (
		id INTEGER PRIMARY KEY,
		req_id INTEGER REFERENCES http_requests(id) ON DELETE CASCADE,
		proto TEXT,
		status_code INTEGER,
		status_reason TEXT,
		body BLOB,
		timestamp DATETIME
	)`)
	if err != nil {
		return fmt.Errorf("could not create http_responses table: %v", err)
	}

	_, err = c.db.Exec(`CREATE TABLE IF NOT EXISTS http_headers (
		id INTEGER PRIMARY KEY,
		req_id INTEGER REFERENCES http_requests(id) ON DELETE CASCADE,
		res_id INTEGER REFERENCES http_responses(id) ON DELETE CASCADE,
		key TEXT,
		value TEXT
	)`)
	if err != nil {
		return fmt.Errorf("could not create http_headers table: %v", err)
	}

	return nil
}

// Close uses the underlying database.
func (c *Client) Close() error {
	return c.db.Close()
}

var reqFieldToColumnMap = map[string]string{
	"proto":     "proto AS req_proto",
	"url":       "url",
	"method":    "method",
	"body":      "body AS req_body",
	"timestamp": "timestamp AS req_timestamp",
}

var resFieldToColumnMap = map[string]string{
	"requestId":    "req_id AS res_req_id",
	"proto":        "proto AS res_proto",
	"statusCode":   "status_code",
	"statusReason": "status_reason",
	"body":         "body AS res_body",
	"timestamp":    "timestamp AS res_timestamp",
}

var headerFieldToColumnMap = map[string]string{
	"key":   "key",
	"value": "value",
}

func (c *Client) FindRequestLogs(
	ctx context.Context,
	opts reqlog.FindRequestsOptions,
	scope *scope.Scope,
) (reqLogs []reqlog.Request, err error) {
	httpReqLogsQuery := parseHTTPRequestLogsQuery(ctx)

	reqQuery := sq.
		Select(httpReqLogsQuery.requestCols...).
		From("http_requests req").
		OrderBy("req.id DESC")
	if httpReqLogsQuery.joinResponse {
		reqQuery = reqQuery.LeftJoin("http_responses res ON req.id = res.req_id")
	}

	sql, _, err := reqQuery.ToSql()
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not parse query: %v", err)
	}

	rows, err := c.db.QueryxContext(ctx, sql, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not execute query: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var dto httpRequest
		err = rows.StructScan(&dto)
		if err != nil {
			return nil, fmt.Errorf("sqlite: could not scan row: %v", err)
		}
		reqLogs = append(reqLogs, dto.toRequestLog())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: could not iterate over rows: %v", err)
	}
	rows.Close()

	if err := c.queryHeaders(ctx, httpReqLogsQuery, reqLogs); err != nil {
		return nil, fmt.Errorf("sqlite: could not query headers: %v", err)
	}

	return reqLogs, nil
}

func (c *Client) FindRequestLogByID(ctx context.Context, id int64) (reqlog.Request, error) {
	httpReqLogsQuery := parseHTTPRequestLogsQuery(ctx)

	reqQuery := sq.
		Select(httpReqLogsQuery.requestCols...).
		From("http_requests req").
		Where("req.id = ?")
	if httpReqLogsQuery.joinResponse {
		reqQuery = reqQuery.LeftJoin("http_responses res ON req.id = res.req_id")
	}

	reqSQL, _, err := reqQuery.ToSql()
	if err != nil {
		return reqlog.Request{}, fmt.Errorf("sqlite: could not parse query: %v", err)
	}

	row := c.db.QueryRowxContext(ctx, reqSQL, id)
	var dto httpRequest
	err = row.StructScan(&dto)
	if err == sql.ErrNoRows {
		return reqlog.Request{}, reqlog.ErrRequestNotFound
	}
	if err != nil {
		return reqlog.Request{}, fmt.Errorf("sqlite: could not scan row: %v", err)
	}
	reqLog := dto.toRequestLog()

	reqLogs := []reqlog.Request{reqLog}
	if err := c.queryHeaders(ctx, httpReqLogsQuery, reqLogs); err != nil {
		return reqlog.Request{}, fmt.Errorf("sqlite: could not query headers: %v", err)
	}

	return reqLogs[0], nil
}

func (c *Client) AddRequestLog(
	ctx context.Context,
	req http.Request,
	body []byte,
	timestamp time.Time,
) (*reqlog.Request, error) {

	reqLog := &reqlog.Request{
		Request:   req,
		Body:      body,
		Timestamp: timestamp,
	}

	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not start transaction: %v", err)
	}
	defer tx.Rollback()

	reqStmt, err := tx.PrepareContext(ctx, `INSERT INTO http_requests (
		proto,
		url,
		method,
		body,
		timestamp
	) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not prepare statement: %v", err)
	}
	defer reqStmt.Close()

	result, err := reqStmt.ExecContext(ctx,
		reqLog.Request.Proto,
		reqLog.Request.URL.String(),
		reqLog.Request.Method,
		reqLog.Body,
		reqLog.Timestamp,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not execute statement: %v", err)
	}

	reqID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not get last insert ID: %v", err)
	}
	reqLog.ID = reqID

	headerStmt, err := tx.PrepareContext(ctx, `INSERT INTO http_headers (
		req_id,
		key,
		value
	) VALUES (?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not prepare statement: %v", err)
	}
	defer headerStmt.Close()

	err = insertHeaders(ctx, headerStmt, reqID, reqLog.Request.Header)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not insert http headers: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: could not commit transaction: %v", err)
	}

	return reqLog, nil
}

func (c *Client) AddResponseLog(
	ctx context.Context,
	reqID int64,
	res http.Response,
	body []byte,
	timestamp time.Time,
) (*reqlog.Response, error) {
	resLog := &reqlog.Response{
		RequestID: reqID,
		Response:  res,
		Body:      body,
		Timestamp: timestamp,
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not start transaction: %v", err)
	}
	defer tx.Rollback()

	resStmt, err := tx.PrepareContext(ctx, `INSERT INTO http_responses (
		req_id,
		proto,
		status_code,
		status_reason,
		body,
		timestamp
	) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not prepare statement: %v", err)
	}
	defer resStmt.Close()

	var statusReason string
	if len(resLog.Response.Status) > 4 {
		statusReason = resLog.Response.Status[4:]
	}

	result, err := resStmt.ExecContext(ctx,
		resLog.RequestID,
		resLog.Response.Proto,
		resLog.Response.StatusCode,
		statusReason,
		resLog.Body,
		resLog.Timestamp,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not execute statement: %v", err)
	}

	resID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not get last insert ID: %v", err)
	}
	resLog.ID = resID

	headerStmt, err := tx.PrepareContext(ctx, `INSERT INTO http_headers (
		res_id,
		key,
		value
	) VALUES (?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not prepare statement: %v", err)
	}
	defer headerStmt.Close()

	err = insertHeaders(ctx, headerStmt, resID, resLog.Response.Header)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not insert http headers: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: could not commit transaction: %v", err)
	}

	return resLog, nil
}

func insertHeaders(ctx context.Context, stmt *sql.Stmt, id int64, headers http.Header) error {
	for key, values := range headers {
		for _, value := range values {
			if _, err := stmt.ExecContext(ctx, id, key, value); err != nil {
				return fmt.Errorf("could not execute statement: %v", err)
			}
		}
	}
	return nil
}

func findHeaders(ctx context.Context, stmt *sql.Stmt, id int64) (http.Header, error) {
	headers := make(http.Header)
	rows, err := stmt.QueryContext(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: could not execute query: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		err := rows.Scan(
			&key,
			&value,
		)
		if err != nil {
			return nil, fmt.Errorf("sqlite: could not scan row: %v", err)
		}
		headers.Add(key, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: could not iterate over rows: %v", err)
	}

	return headers, nil
}

func parseHTTPRequestLogsQuery(ctx context.Context) httpRequestLogsQuery {
	var joinResponse bool
	var reqHeaderCols, resHeaderCols []string

	opCtx := graphql.GetOperationContext(ctx)
	reqFields := graphql.CollectFieldsCtx(ctx, nil)
	reqCols := []string{"req.id AS req_id", "res.id AS res_id"}

	for _, reqField := range reqFields {
		if col, ok := reqFieldToColumnMap[reqField.Name]; ok {
			reqCols = append(reqCols, "req."+col)
		}
		if reqField.Name == "headers" {
			headerFields := graphql.CollectFields(opCtx, reqField.Selections, nil)
			for _, headerField := range headerFields {
				if col, ok := headerFieldToColumnMap[headerField.Name]; ok {
					reqHeaderCols = append(reqHeaderCols, col)
				}
			}
		}
		if reqField.Name == "response" {
			joinResponse = true
			resFields := graphql.CollectFields(opCtx, reqField.Selections, nil)
			for _, resField := range resFields {
				if resField.Name == "headers" {
					reqCols = append(reqCols, "res.id AS res_id")
					headerFields := graphql.CollectFields(opCtx, resField.Selections, nil)
					for _, headerField := range headerFields {
						if col, ok := headerFieldToColumnMap[headerField.Name]; ok {
							resHeaderCols = append(resHeaderCols, col)
						}
					}
				}
				if col, ok := resFieldToColumnMap[resField.Name]; ok {
					reqCols = append(reqCols, "res."+col)
				}
			}
		}
	}

	return httpRequestLogsQuery{
		requestCols:        reqCols,
		requestHeaderCols:  reqHeaderCols,
		responseHeaderCols: resHeaderCols,
		joinResponse:       joinResponse,
	}
}

func (c *Client) queryHeaders(
	ctx context.Context,
	query httpRequestLogsQuery,
	reqLogs []reqlog.Request,
) error {
	if len(query.requestHeaderCols) > 0 {
		reqHeadersQuery, _, err := sq.
			Select(query.requestHeaderCols...).
			From("http_headers").Where("req_id = ?").
			ToSql()
		if err != nil {
			return fmt.Errorf("could not parse request headers query: %v", err)
		}
		reqHeadersStmt, err := c.db.PrepareContext(ctx, reqHeadersQuery)
		if err != nil {
			return fmt.Errorf("could not prepare statement: %v", err)
		}
		defer reqHeadersStmt.Close()
		for i := range reqLogs {
			headers, err := findHeaders(ctx, reqHeadersStmt, reqLogs[i].ID)
			if err != nil {
				return fmt.Errorf("could not query request headers: %v", err)
			}
			reqLogs[i].Request.Header = headers
		}
	}

	if len(query.responseHeaderCols) > 0 {
		resHeadersQuery, _, err := sq.
			Select(query.responseHeaderCols...).
			From("http_headers").Where("res_id = ?").
			ToSql()
		if err != nil {
			return fmt.Errorf("could not parse response headers query: %v", err)
		}
		resHeadersStmt, err := c.db.PrepareContext(ctx, resHeadersQuery)
		if err != nil {
			return fmt.Errorf("could not prepare statement: %v", err)
		}
		defer resHeadersStmt.Close()
		for i := range reqLogs {
			if reqLogs[i].Response == nil {
				continue
			}
			headers, err := findHeaders(ctx, resHeadersStmt, reqLogs[i].Response.ID)
			if err != nil {
				return fmt.Errorf("could not query response headers: %v", err)
			}
			reqLogs[i].Response.Response.Header = headers
		}
	}

	return nil
}
