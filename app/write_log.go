// Copyright 2013 The Gorilla Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package app

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/ironsmile/nedomi/types"
)

// The file is mostly a copy of the source from gorilla's handlers.go

// buildCommonLogLine builds a log entry for req in Apache Common Log Format.
// ts is the timestamp with which the entry should be logged.
// status and size are used to provide the response HTTP status and size.
// Additionally the time since the timestamp is being written
func buildCommonLogLine(
	req *http.Request,
	locationIdentification string,
	reqID types.RequestID,
	url url.URL,
	ts time.Time,
	status int, size uint64,
) []byte {
	username := "-"
	if url.User != nil {
		if name := url.User.Username(); name != "" {
			username = name
		}
	}

	host, _, err := net.SplitHostPort(req.RemoteAddr)

	if err != nil {
		host = req.RemoteAddr
	}

	uri := url.RequestURI()
	ranFor := int(time.Since(ts).Nanoseconds())
	bufSize := 3 * (len(host) + len(username) + len(req.Method) + len(uri) +
		len(req.Proto) + len(locationIdentification) + len(reqID) + 54) / 2

	buf := make([]byte, 0, bufSize)
	buf = append(buf, host...)
	buf = append(buf, " -> "...)
	buf = append(buf, locationIdentification...)
	buf = append(buf, ' ')
	buf = append(buf, reqID...)
	buf = append(buf, " - "...)
	buf = append(buf, username...)
	buf = append(buf, " ["...)
	buf = append(buf, ts.Format("02/Jan/2006:15:04:05 -0700")...)
	buf = append(buf, `] "`...)
	buf = append(buf, req.Method...)
	buf = append(buf, " "...)
	buf = appendQuoted(buf, uri)
	buf = append(buf, " "...)
	buf = append(buf, req.Proto...)
	buf = append(buf, `" `...)
	buf = append(buf, strconv.Itoa(status)...)
	buf = append(buf, " "...)
	buf = append(buf, strconv.FormatUint(size, 10)...)
	buf = append(buf, " "...)
	buf = append(buf, strconv.Itoa(ranFor)...)
	return buf
}

// writeLog writes a log entry for req to w in Apache Common Log Format.
// ts is the timestamp with which the entry should be logged.
// status and size are used to provide the response HTTP status and size.
func writeLog(
	w io.Writer,
	req *http.Request,
	locationIdentification string,
	reqID types.RequestID,
	url url.URL,
	ts time.Time,
	status int, size uint64,
) {
	buf := buildCommonLogLine(req, locationIdentification, reqID, url, ts, status, size)
	buf = append(buf, '\n')
	_, _ = w.Write(buf)
}

func appendQuoted(buf []byte, s string) []byte {
	var runeTmp [utf8.UTFMax]byte
	for width := 0; len(s) > 0; s = s[width:] {
		r := rune(s[0])
		width = 1
		if r >= utf8.RuneSelf {
			r, width = utf8.DecodeRuneInString(s)
		}
		if width == 1 && r == utf8.RuneError {
			buf = append(buf, `\x`...)
			buf = append(buf, lowerhex[s[0]>>4])
			buf = append(buf, lowerhex[s[0]&0xF])
			continue
		}
		if r == rune('"') || r == '\\' { // always backslashed
			buf = append(buf, '\\')
			buf = append(buf, byte(r))
			continue
		}
		if strconv.IsPrint(r) {
			n := utf8.EncodeRune(runeTmp[:], r)
			buf = append(buf, runeTmp[:n]...)
			continue
		}
		switch r {
		case '\a':
			buf = append(buf, `\a`...)
		case '\b':
			buf = append(buf, `\b`...)
		case '\f':
			buf = append(buf, `\f`...)
		case '\n':
			buf = append(buf, `\n`...)
		case '\r':
			buf = append(buf, `\r`...)
		case '\t':
			buf = append(buf, `\t`...)
		case '\v':
			buf = append(buf, `\v`...)
		default:
			switch {
			case r < ' ':
				buf = append(buf, `\x`...)
				buf = append(buf, lowerhex[s[0]>>4])
				buf = append(buf, lowerhex[s[0]&0xF])
			case r > utf8.MaxRune:
				r = 0xFFFD
				fallthrough
			case r < 0x10000:
				buf = append(buf, `\u`...)
				for s := 12; s >= 0; s -= 4 {
					buf = append(buf, lowerhex[r>>uint(s)&0xF])
				}
			default:
				buf = append(buf, `\U`...)
				for s := 28; s >= 0; s -= 4 {
					buf = append(buf, lowerhex[r>>uint(s)&0xF])
				}
			}
		}
	}
	return buf
}

const lowerhex = "0123456789abcdef"

type responseLogger struct {
	http.ResponseWriter
	status int
	size   uint64
}

func (l *responseLogger) Write(b []byte) (n int, err error) {
	if l.status == 0 {
		// The status will be StatusOK if WriteHeader has not been called yet
		l.status = http.StatusOK
	}
	n, err = l.ResponseWriter.Write(b)
	atomic.AddUint64(&l.size, uint64(n))
	return n, err
}

func (l *responseLogger) WriteHeader(s int) {
	l.ResponseWriter.WriteHeader(s)
	l.status = s
}

func (l *responseLogger) Status() int {
	return l.status
}

func (l *responseLogger) Size() uint64 {
	return l.size
}

func (l *responseLogger) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = io.Copy(l.ResponseWriter, r)
	atomic.AddUint64(&l.size, uint64(n))
	return n, err
}
