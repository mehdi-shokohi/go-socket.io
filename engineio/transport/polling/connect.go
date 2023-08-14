package polling

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/thisismz/go-socket.io/v4/engineio/packet"
	"github.com/thisismz/go-socket.io/v4/engineio/payload"
	"github.com/thisismz/go-socket.io/v4/engineio/transport"
	"github.com/thisismz/go-socket.io/v4/engineio/transport/utils"
	"github.com/thisismz/go-socket.io/v4/logger"
)

type clientConn struct {
	*payload.Payload

	httpClient   *http.Client
	request      http.Request
	remoteHeader atomic.Value
}

func (c *clientConn) Open() (transport.ConnParameters, error) {
	go c.getOpen()

	_, pt, r, err := c.NextReader()
	if err != nil {
		return transport.ConnParameters{}, err
	}

	if pt != packet.OPEN {
		_ = r.Close()
		return transport.ConnParameters{}, errors.New("invalid open")
	}

	conn, err := transport.ReadConnParameters(r)
	if err != nil {
		_ = r.Close()
		return transport.ConnParameters{}, err
	}

	err = r.Close()

	if err != nil {
		return transport.ConnParameters{}, err
	}

	query := c.request.URL.Query()
	query.Set("sid", conn.SID)
	c.request.URL.RawQuery = query.Encode()

	go c.serveGet()
	go c.servePost()

	return conn, nil
}

func (c *clientConn) URL() url.URL {
	return *c.request.URL
}

func (c *clientConn) LocalAddr() net.Addr {
	return Addr{""}
}

func (c *clientConn) RemoteAddr() net.Addr {
	return Addr{c.request.Host}
}

func (c *clientConn) RemoteHeader() http.Header {
	ret := c.remoteHeader.Load()
	if ret == nil {
		return nil
	}
	return ret.(http.Header)
}

func (c *clientConn) Resume() {
	c.Payload.Resume()

	go c.serveGet()
	go c.servePost()
}

func (c *clientConn) servePost() {
	req := c.request
	reqUrl := *req.URL

	req.URL = &reqUrl
	req.Method = http.MethodPost

	var buf bytes.Buffer
	req.Body = io.NopCloser(&buf)
	var ll = logger.GetLogger("engineio.transport.polling")

	query := reqUrl.Query()
	for {
		buf.Reset()

		if err := c.Payload.FlushOut(&buf); err != nil {
			return
		}
		query.Set("t", utils.Timestamp())
		req.URL.RawQuery = query.Encode()

		resp, err := c.httpClient.Do(&req)
		if err != nil {
			if err = c.Payload.Store("post", err); err != nil {
				ll.Error(err, "Store post error")
			}
			_ = c.Close()
			return
		}

		discardBody(resp.Body)

		if resp.StatusCode != http.StatusOK {
			err = c.Payload.Store("post", fmt.Errorf("invalid response: %s(%d)", resp.Status, resp.StatusCode))
			if err != nil {
				ll.Error(err, "Store post error")
			}
			_ = c.Close()
			return
		}

		c.remoteHeader.Store(resp.Header)
	}
}

func (c *clientConn) getOpen() {
	req := c.request
	query := req.URL.Query()

	reqUrl := *req.URL
	req.URL = &reqUrl
	req.Method = http.MethodGet

	query.Set("t", utils.Timestamp())
	req.URL.RawQuery = query.Encode()
	var ll = logger.GetLogger("engineio.transport.polling")
	resp, err := c.httpClient.Do(&req)
	if err != nil {
		if err = c.Payload.Store("get", err); err != nil {
			ll.Error(err, "Store get error")
		}

		_ = c.Close()
		return
	}

	defer func() {
		discardBody(resp.Body)
	}()

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("invalid request: %s(%d)", resp.Status, resp.StatusCode)
	}

	var isSupportBinary bool
	if err == nil {
		mime := resp.Header.Get("Content-Type")
		isSupportBinary, err = mimeIsSupportBinary(mime)
		if err != nil {
			ll.Error(err, "Check mime support binary")
		}
	}

	if err != nil {
		if err = c.Payload.Store("get", err); err != nil {
			ll.Error(err, "Store get error")
		}
		_ = c.Close()

		return
	}

	c.remoteHeader.Store(resp.Header)

	if err = c.Payload.FeedIn(resp.Body, isSupportBinary); err != nil {
		return
	}
}

func (c *clientConn) serveGet() {
	req := c.request
	reqUrl := *req.URL

	req.URL = &reqUrl
	req.Method = http.MethodGet
	var ll = logger.GetLogger("engineio.transport.polling")
	query := req.URL.Query()
	for {
		query.Set("t", utils.Timestamp())
		req.URL.RawQuery = query.Encode()

		resp, err := c.httpClient.Do(&req)
		if err != nil {
			if err = c.Payload.Store("get", err); err != nil {
				ll.Error(err, "Store get error")
			}
			_ = c.Close()

			return
		}

		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("invalid request: %s(%d)", resp.Status, resp.StatusCode)
		}

		var isSupportBinary bool
		if err == nil {
			mime := resp.Header.Get("Content-Type")
			isSupportBinary, err = mimeIsSupportBinary(mime)
			if err != nil {
				ll.Error(err, "Check mime support binary")
			}
		}

		if err != nil {
			discardBody(resp.Body)

			if err = c.Payload.Store("get", err); err != nil {
				ll.Error(err, "Store get error")
			}

			_ = c.Close()

			return
		}

		if err = c.Payload.FeedIn(resp.Body, isSupportBinary); err != nil {
			discardBody(resp.Body)

			return
		}

		c.remoteHeader.Store(resp.Header)
	}
}

func discardBody(body io.ReadCloser) {
	var ll = logger.GetLogger("engineio.transport.polling")
	_, err := io.Copy(io.Discard, body)
	if err != nil {
		ll.Error(err, "Copy from body resp to discard")
	}
	_ = body.Close()
}
