// fingerprint is a loopback-only fixture for destination-observed browser tests.
// It is not bundled with the plugin.
package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

type extension struct {
	ID   uint16 `json:"id"`
	Data string `json:"data"`
}

type hello struct {
	Ciphers    []uint16    `json:"ciphers"`
	Extensions []extension `json:"extensions"`
}

type setting struct {
	ID    uint16 `json:"id"`
	Value uint32 `json:"value"`
}

type observation struct {
	TLS      hello       `json:"tls"`
	Protocol string      `json:"protocol"`
	Resumed  bool        `json:"resumed"`
	Settings []setting   `json:"settings,omitempty"`
	Window   uint32      `json:"window,omitempty"`
	Headers  [][2]string `json:"headers"`
	Path     string      `json:"path"`
}

type captureConn struct {
	net.Conn
	data []byte
}

func (conn *captureConn) Read(p []byte) (int, error) {
	n, err := conn.Conn.Read(p)
	if len(conn.data)+n <= 256*1024 {
		conn.data = append(conn.data, p[:n]...)
	}
	return n, err
}

func parseHello(records []byte) (hello, error) {
	var handshake []byte
	for len(records) >= 5 {
		size := int(binary.BigEndian.Uint16(records[3:5]))
		if len(records) < 5+size {
			break
		}
		if records[0] == 22 {
			handshake = append(handshake, records[5:5+size]...)
		}
		records = records[5+size:]
		if len(handshake) >= 4 {
			size := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
			if handshake[0] != 1 {
				break
			}
			if len(handshake) >= 4+size {
				body := cryptobyte.String(handshake[4 : 4+size])
				var session, ciphers, compression, extensions cryptobyte.String
				if !body.Skip(34) || !body.ReadUint8LengthPrefixed(&session) || !body.ReadUint16LengthPrefixed(&ciphers) ||
					!body.ReadUint8LengthPrefixed(&compression) || !body.ReadUint16LengthPrefixed(&extensions) || !body.Empty() {
					return hello{}, fmt.Errorf("malformed ClientHello")
				}
				result := hello{}
				for !ciphers.Empty() {
					var cipher uint16
					if !ciphers.ReadUint16(&cipher) {
						return hello{}, io.ErrUnexpectedEOF
					}
					result.Ciphers = append(result.Ciphers, cipher)
				}
				for !extensions.Empty() {
					var id uint16
					var data cryptobyte.String
					if !extensions.ReadUint16(&id) || !extensions.ReadUint16LengthPrefixed(&data) {
						return hello{}, io.ErrUnexpectedEOF
					}
					result.Extensions = append(result.Extensions, extension{id, hex.EncodeToString(data)})
				}
				return result, nil
			}
		}
	}
	return hello{}, fmt.Errorf("incomplete ClientHello")
}

func certificate(caPath string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Impersonate TLS test fixture"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

func serveH2(conn *tls.Conn, base observation) error {
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return fmt.Errorf("invalid h2 preface")
	}
	framer := http2.NewFramer(conn, conn)
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	framer.MaxHeaderListSize = 256 * 1024
	if err := framer.WriteSettings(); err != nil {
		return err
	}
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			return err
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				if base.Settings == nil {
					base.Settings = []setting{}
					if err := frame.ForeachSetting(func(item http2.Setting) error {
						base.Settings = append(base.Settings, setting{uint16(item.ID), item.Val})
						return nil
					}); err != nil {
						return err
					}
				}
				if err := framer.WriteSettingsAck(); err != nil {
					return err
				}
			}
		case *http2.WindowUpdateFrame:
			if frame.StreamID == 0 && base.Window == 0 {
				base.Window = frame.Increment
			}
		case *http2.PingFrame:
			if !frame.IsAck() {
				if err := framer.WritePing(true, frame.Data); err != nil {
					return err
				}
			}
		case *http2.MetaHeadersFrame:
			result := base
			result.Headers = nil
			for _, field := range frame.Fields {
				result.Headers = append(result.Headers, [2]string{field.Name, field.Value})
				if field.Name == ":path" {
					result.Path = field.Value
				}
			}
			body, err := json.Marshal(result)
			if err != nil {
				return err
			}
			var headers bytes.Buffer
			encoder := hpack.NewEncoder(&headers)
			for _, field := range [][2]string{{":status", "200"}, {"content-type", "application/json"}, {"cache-control", "no-store"}, {"content-length", fmt.Sprint(len(body))}} {
				if err := encoder.WriteField(hpack.HeaderField{Name: field[0], Value: field[1], Sensitive: true}); err != nil {
					return err
				}
			}
			if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: frame.StreamID, BlockFragment: headers.Bytes(), EndHeaders: true}); err != nil {
				return err
			}
			if err := framer.WriteData(frame.StreamID, true, body); err != nil {
				return err
			}
			if strings.HasPrefix(result.Path, "/fresh-") {
				if err := framer.WriteGoAway(frame.StreamID, http2.ErrCodeNo, nil); err != nil {
					return err
				}
				return nil
			}
		case *http2.GoAwayFrame:
			return nil
		}
	}
}

func serveH1(conn *tls.Conn, base observation) error {
	reader := bufio.NewReader(conn)
	var raw strings.Builder
	for raw.Len() < 256*1024 {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		raw.WriteString(line)
		if line == "\r\n" {
			break
		}
		if name, value, found := strings.Cut(strings.TrimSpace(line), ":"); found {
			base.Headers = append(base.Headers, [2]string{name, strings.TrimSpace(value)})
		}
	}
	request, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw.String())))
	if err != nil {
		return err
	}
	base.Path = request.RequestURI
	body, err := json.Marshal(base)
	if err != nil {
		return err
	}
	if strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
		accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:])); err != nil {
			return err
		}
		frame := []byte{0x81, 126, byte(len(body) >> 8), byte(len(body))}
		_, err = conn.Write(append(frame, body...))
		return err
	}
	_, err = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
	return err
}

func main() {
	caPath := flag.String("ca-file", "", "where to write the fixture CA")
	flag.Parse()
	if *caPath == "" {
		panic("ca-file is required")
	}
	cert, err := certificate(*caPath)
	if err != nil {
		panic(err)
	}
	config := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"port": listener.Addr().(*net.TCPAddr).Port, "caFile": *caPath})
	for {
		raw, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		go func() {
			defer raw.Close()
			_ = raw.SetDeadline(time.Now().Add(30 * time.Second))
			capture := &captureConn{Conn: raw}
			conn := tls.Server(capture, config)
			if err := conn.Handshake(); err != nil {
				return
			}
			fingerprint, err := parseHello(capture.data)
			if err != nil {
				return
			}
			state := conn.ConnectionState()
			observation := observation{TLS: fingerprint, Protocol: state.NegotiatedProtocol, Resumed: state.DidResume}
			if observation.Protocol == "h2" {
				_ = serveH2(conn, observation)
			} else {
				_ = serveH1(conn, observation)
			}
		}()
	}
}
