package relay

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

func writeResponse(writer io.Writer, method string, response *fhttp.Response) error {
	defer response.Body.Close()

	buffered := bufio.NewWriterSize(writer, 64*1024)
	status := response.Status
	if status == "" {
		status = fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode))
	}
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 %s\r\n", status); err != nil {
		return err
	}

	for name, values := range response.Header {
		lowerName := strings.ToLower(name)
		if lowerName == "connection" ||
			lowerName == "content-length" ||
			lowerName == "transfer-encoding" ||
			lowerName == "trailer" {
			continue
		}
		for _, value := range values {
			if _, err := fmt.Fprintf(buffered, "%s: %s\r\n", name, value); err != nil {
				return err
			}
		}
	}

	hasBody := method != http.MethodHead &&
		response.StatusCode >= 200 &&
		response.StatusCode != http.StatusNoContent &&
		response.StatusCode != http.StatusNotModified

	if !hasBody {
		if response.ContentLength >= 0 {
			if _, err := fmt.Fprintf(buffered, "Content-Length: %d\r\n", response.ContentLength); err != nil {
				return err
			}
		}
		_, err := io.WriteString(buffered, "Connection: close\r\n\r\n")
		if err != nil {
			return err
		}
		return buffered.Flush()
	}

	if response.ContentLength >= 0 {
		if _, err := fmt.Fprintf(
			buffered,
			"Content-Length: %d\r\nConnection: close\r\n\r\n",
			response.ContentLength,
		); err != nil {
			return err
		}
		if err := buffered.Flush(); err != nil {
			return err
		}
		_, err := io.Copy(writer, response.Body)
		return err
	}

	if _, err := io.WriteString(
		buffered,
		"Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n",
	); err != nil {
		return err
	}
	if err := buffered.Flush(); err != nil {
		return err
	}

	chunked := httputil.NewChunkedWriter(writer)
	_, copyErr := io.Copy(chunked, response.Body)
	closeErr := chunked.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	_, err := io.WriteString(writer, "\r\n")
	return err
}

func writeError(writer io.Writer, statusCode int, message string) {
	body := http.StatusText(statusCode)
	if message != "" {
		body = fmt.Sprintf("%s: %s", body, message)
	}
	_, _ = fmt.Fprintf(
		writer,
		"HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		statusCode,
		http.StatusText(statusCode),
		len(body),
		body,
	)
}
