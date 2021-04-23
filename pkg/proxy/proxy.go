package proxy

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"

	"github.com/dstotijn/hetty/pkg/scope"
)

type contextKey int

const ReqIDKey contextKey = 0

// Proxy implements http.Handler and offers MITM behaviour for modifying
// HTTP requests and responses.
type Proxy struct {
	certConfig *CertConfig
	handler    http.Handler

	// TODO: Add mutex for modifier funcs.
	reqModifiers []RequestModifyMiddleware
	resModifiers []ResponseModifyMiddleware

	scope *scope.Scope
}

// NewProxy returns a new Proxy.
func NewProxy(ca *x509.Certificate, key crypto.PrivateKey) (*Proxy, error) {
	certConfig, err := NewCertConfig(ca, key)
	if err != nil {
		return nil, err
	}

	p := &Proxy{
		certConfig:   certConfig,
		reqModifiers: make([]RequestModifyMiddleware, 0),
		resModifiers: make([]ResponseModifyMiddleware, 0),
	}

	p.handler = &httputil.ReverseProxy{
		Director:       p.modifyRequest,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   errorHandler,
	}

	intercept, err := NewIntercept(GetRequestsFromYaml, GetResponsesFromYaml)

	if err != nil {
		return nil, err
	}

	p.UseRequestModifier(intercept.RequestInterceptor)
	p.UseResponseModifier(intercept.ResponseInterceptor)

	return p, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}

	p.handler.ServeHTTP(w, r)
}

func (p *Proxy) UseRequestModifier(fn ...RequestModifyMiddleware) {
	p.reqModifiers = append(p.reqModifiers, fn...)
}

func (p *Proxy) UseResponseModifier(fn ...ResponseModifyMiddleware) {
	p.resModifiers = append(p.resModifiers, fn...)
}

func (p *Proxy) modifyRequest(r *http.Request) {
	// Fix r.URL for HTTPS requests after CONNECT.
	if r.URL.Scheme == "" {
		r.URL.Host = r.Host
		r.URL.Scheme = "https"
	}

	// Setting `X-Forwarded-For` to `nil` ensures that http.ReverseProxy doesn't
	// set this header.
	r.Header["X-Forwarded-For"] = nil

	fn := nopReqModifier

	for i := len(p.reqModifiers) - 1; i >= 0; i-- {
		fn = p.reqModifiers[i](fn)
	}

	fn(r)
}

func (p *Proxy) modifyResponse(res *http.Response) error {
	fn := nopResModifier

	for i := len(p.resModifiers) - 1; i >= 0; i-- {
		fn = p.resModifiers[i](fn)
	}

	return fn(res)
}

// handleConnect hijacks the incoming HTTP request and sets up an HTTP tunnel.
// During the TLS handshake with the client, we use the proxy's CA config to
// create a certificate on-the-fly.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("[ERROR] handleConnect: ResponseWriter is not a http.Hijacker (type: %T)", w)
		writeError(w, r, http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)

	clientConn, _, err := hj.Hijack()
	if err != nil {
		log.Printf("[ERROR] Hijacking client connection failed: %v", err)
		writeError(w, r, http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	// Secure connection to client.
	clientConn, err = p.clientTLSConn(clientConn)
	if err != nil {
		log.Printf("[ERROR] Securing client connection failed: %v", err)
		return
	}
	clientConnNotify := ConnNotify{clientConn, make(chan struct{})}

	l := &OnceAcceptListener{clientConnNotify.Conn}

	err = http.Serve(l, p)
	if err != nil && err != ErrAlreadyAccepted {
		log.Printf("[ERROR] Serving HTTP request failed: %v", err)
	}
	<-clientConnNotify.closed
}

func (p *Proxy) clientTLSConn(conn net.Conn) (*tls.Conn, error) {
	tlsConfig := p.certConfig.TLSConfig()

	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("handshake error: %v", err)
	}

	return tlsConn, nil
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if err == context.Canceled {
		return
	}
	log.Printf("[ERROR]: Proxy error: %v", err)
	w.WriteHeader(http.StatusBadGateway)
}

func writeError(w http.ResponseWriter, r *http.Request, code int) {
	http.Error(w, http.StatusText(code), code)
}
