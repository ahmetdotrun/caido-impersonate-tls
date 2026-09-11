package relay

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxRequestLineBytes = 16 * 1024
	maxHeaderBytes      = 256 * 1024
)

const (
	headerToken   = "X-Caido-Impersonate-Token"
	headerScheme  = "X-Caido-Impersonate-Scheme"
	headerHost    = "X-Caido-Impersonate-Host"
	headerPort    = "X-Caido-Impersonate-Port"
	headerProfile = "X-Caido-Impersonate-Profile"
	headerTrace   = "X-Caido-Impersonate-Trace"
	headerMaxBody = "X-Caido-Impersonate-Max-Body-Bytes"
)

type header struct {
	Name  string
	Value string
}

type incomingRequest struct {
	Method     string
	RequestURI string
	Protocol   string
	Headers    []header
	Body       io.Reader
	BodyLength int64
}

type routeMetadata struct {
	Token   string
	Scheme  string
	Host    string
	Port    string
	Profile string
	Trace   string
}

func readIncomingRequest(reader *bufio.Reader) (*incomingRequest, error) {
	request, err := readIncomingHead(reader)
	if err != nil {
		return nil, err
	}
	if err := request.prepareBody(reader); err != nil {
		return nil, err
	}
	return request, nil
}

func readIncomingHead(reader *bufio.Reader) (*incomingRequest, error) {
	requestLine, err := readLimitedLine(reader, maxRequestLineBytes)
	if err != nil {
		return nil, fmt.Errorf("request line: %w", err)
	}

	parts := strings.Fields(requestLine)
	if len(parts) != 3 {
		return nil, errors.New("malformed request line")
	}
	if validToken(parts[0]) == false {
		return nil, errors.New("invalid request method")
	}
	if strings.EqualFold(parts[0], "CONNECT") {
		return nil, errors.New("CONNECT is not supported by the private transport")
	}
	if parts[2] != "HTTP/1.1" && parts[2] != "HTTP/1.0" {
		return nil, fmt.Errorf("unsupported protocol %q", parts[2])
	}

	request := &incomingRequest{
		Method:     parts[0],
		RequestURI: parts[1],
		Protocol:   parts[2],
	}

	headerBytes := 0
	for {
		line, lineErr := readLimitedLine(reader, maxHeaderBytes)
		if lineErr != nil {
			return nil, fmt.Errorf("headers: %w", lineErr)
		}
		headerBytes += len(line) + 2
		if headerBytes > maxHeaderBytes {
			return nil, errors.New("headers exceed limit")
		}
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			return nil, errors.New("obsolete folded headers are not supported")
		}

		name, value, found := strings.Cut(line, ":")
		if !found || validToken(name) == false {
			return nil, errors.New("malformed header")
		}
		value = strings.TrimSpace(value)
		if validHeaderValue(value) == false {
			return nil, errors.New("invalid header value")
		}
		request.Headers = append(request.Headers, header{
			Name:  name,
			Value: value,
		})
	}

	return request, nil
}

// prepareBody validates framing without consuming the upload. The HTTP client
// pulls bytes only as it can forward them, keeping memory independent of size.
func (request *incomingRequest) prepareBody(reader *bufio.Reader) error {
	contentLength, hasContentLength, err := request.contentLength()
	if err != nil {
		return err
	}
	transferEncodingValues := request.headerValues("Transfer-Encoding")
	chunked := false
	if len(transferEncodingValues) > 0 {
		if len(transferEncodingValues) != 1 ||
			!strings.EqualFold(strings.TrimSpace(transferEncodingValues[0]), "chunked") {
			return errors.New("unsupported Transfer-Encoding")
		}
		chunked = true
	}
	if chunked && hasContentLength {
		return errors.New("both Transfer-Encoding and Content-Length are present")
	}
	var maximum int64
	if values := request.headerValues(headerMaxBody); len(values) > 0 {
		if len(values) != 1 {
			return errors.New("multiple upload limit values")
		}
		maximum, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || maximum < 0 {
			return errors.New("invalid upload limit")
		}
	}

	switch {
	case chunked:
		request.Body = httputil.NewChunkedReader(reader)
		request.BodyLength = -1
	case hasContentLength && contentLength > 0:
		if maximum > 0 && contentLength > maximum {
			return errBodyTooLarge
		}
		request.Body = &exactBodyReader{reader: reader, remaining: contentLength}
		request.BodyLength = contentLength
	}
	if request.Body != nil && maximum > 0 {
		request.Body = &limitedBodyReader{reader: request.Body, remaining: maximum}
	}

	return nil
}

func (request *incomingRequest) metadata() (routeMetadata, error) {
	token, err := request.singleHeader(headerToken)
	if err != nil {
		return routeMetadata{}, err
	}
	scheme, err := request.singleHeader(headerScheme)
	if err != nil {
		return routeMetadata{}, err
	}
	host, err := request.singleHeader(headerHost)
	if err != nil {
		return routeMetadata{}, err
	}
	portValue, err := request.singleHeader(headerPort)
	if err != nil {
		return routeMetadata{}, err
	}
	profile, err := request.singleHeader(headerProfile)
	if err != nil {
		return routeMetadata{}, err
	}
	trace, err := request.singleHeader(headerTrace)
	if err != nil {
		return routeMetadata{}, err
	}

	metadata := routeMetadata{
		Token:   token,
		Scheme:  strings.ToLower(scheme),
		Host:    host,
		Port:    portValue,
		Profile: profile,
		Trace:   trace,
	}

	if metadata.Token == "" {
		return routeMetadata{}, errors.New("missing transport token")
	}
	if metadata.Scheme != "http" && metadata.Scheme != "https" {
		return routeMetadata{}, errors.New("invalid target scheme")
	}
	if metadata.Host == "" || strings.ContainsAny(metadata.Host, "\r\n\t /\\@?#") {
		return routeMetadata{}, errors.New("invalid target host")
	}
	port, err := strconv.ParseUint(metadata.Port, 10, 16)
	if err != nil || port == 0 {
		return routeMetadata{}, errors.New("invalid target port")
	}
	if metadata.Profile == "" {
		return routeMetadata{}, errors.New("missing transport profile")
	}
	if validTraceID(metadata.Trace) == false {
		return routeMetadata{}, errors.New("invalid transport trace")
	}

	return metadata, nil
}

func validTraceID(value string) bool {
	if len(value) < 3 || len(value) > 64 {
		return false
	}

	dashFound := false
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '-' && dashFound == false:
			dashFound = true
		default:
			return false
		}
	}
	return dashFound
}

func (request *incomingRequest) targetPath() (string, error) {
	if request.RequestURI == "*" {
		return "/", nil
	}

	parsed, err := url.ParseRequestURI(request.RequestURI)
	if err != nil {
		return "", fmt.Errorf("invalid request target: %w", err)
	}
	if parsed.IsAbs() {
		return parsed.RequestURI(), nil
	}
	return request.RequestURI, nil
}

func (request *incomingRequest) contentLength() (int64, bool, error) {
	values := request.headerValues("Content-Length")
	if len(values) == 0 {
		return 0, false, nil
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return 0, false, errors.New("multiple Content-Length values")
	}

	length, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || length < 0 {
		return 0, false, errors.New("invalid Content-Length")
	}
	return length, true, nil
}

func (request *incomingRequest) singleHeader(name string) (string, error) {
	values := request.headerValues(name)
	if len(values) == 0 {
		return "", fmt.Errorf("missing %s header", name)
	}
	if len(values) != 1 {
		return "", fmt.Errorf("multiple %s headers", name)
	}
	if values[0] == "" {
		return "", fmt.Errorf("missing %s header", name)
	}
	return values[0], nil
}

func (request *incomingRequest) headerValues(name string) []string {
	values := make([]string, 0, 1)
	for _, candidate := range request.Headers {
		if strings.EqualFold(candidate.Name, name) {
			values = append(values, candidate.Value)
		}
	}
	return values
}

func (request *incomingRequest) firstHeader(name string) string {
	for _, candidate := range request.Headers {
		if strings.EqualFold(candidate.Name, name) {
			return candidate.Value
		}
	}
	return ""
}

func readLimitedLine(reader *bufio.Reader, limit int) (string, error) {
	var output strings.Builder
	for {
		fragment, isPrefix, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		if output.Len()+len(fragment) > limit {
			return "", errors.New("line exceeds limit")
		}
		output.Write(fragment)
		if !isPrefix {
			return output.String(), nil
		}
	}
}

var errBodyTooLarge = errors.New("request body exceeds configured upload limit")

type exactBodyReader struct {
	reader    io.Reader
	remaining int64
}

func (reader *exactBodyReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(buffer)) > reader.remaining {
		buffer = buffer[:reader.remaining]
	}
	n, err := reader.reader.Read(buffer)
	reader.remaining -= int64(n)
	if err == io.EOF && reader.remaining > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

type limitedBodyReader struct {
	reader    io.Reader
	remaining int64
}

func (reader *limitedBodyReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if reader.remaining == 0 {
		var probe [1]byte
		n, err := reader.reader.Read(probe[:])
		if n > 0 {
			return 0, errBodyTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > reader.remaining {
		buffer = buffer[:reader.remaining]
	}
	n, err := reader.reader.Read(buffer)
	reader.remaining -= int64(n)
	return n, err
}

func validToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range []byte(value) {
		if character <= 0x20 || character >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(character)) {
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for _, character := range []byte(value) {
		if (character < 0x20 && character != '\t') || character == 0x7f {
			return false
		}
	}
	return true
}
