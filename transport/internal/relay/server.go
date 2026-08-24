package relay

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"time"
)

const maxConcurrentConnections = 128
const maxActivityErrorBytes = 500

type requestEvent struct {
	Event      string `json:"event"`
	ID         string `json:"id"`
	Outcome    string `json:"outcome"`
	StatusCode int    `json:"statusCode,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

type Server struct {
	token          string
	logger         *log.Logger
	activityLogger *log.Logger
	clients        *clientPool
	slots          chan struct{}
}

func NewServer(token string, logger *log.Logger, activityLoggers ...*log.Logger) *Server {
	server := &Server{
		token:   token,
		logger:  logger,
		clients: newClientPool(),
		slots:   make(chan struct{}, maxConcurrentConnections),
	}
	if len(activityLoggers) > 0 {
		server.activityLogger = activityLoggers[0]
	}
	return server
}

func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case server.slots <- struct{}{}:
			go func() {
				defer func() { <-server.slots }()
				server.handle(connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (server *Server) handle(connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(90 * time.Second))

	reader := bufio.NewReaderSize(connection, 64*1024)
	request, err := readIncomingHead(reader)
	if err != nil {
		writeError(connection, http.StatusBadRequest, err.Error())
		return
	}

	metadata, err := request.metadata()
	if err != nil {
		writeError(connection, http.StatusBadRequest, err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(metadata.Token), []byte(server.token)) != 1 {
		writeError(connection, http.StatusForbidden, "invalid transport token")
		return
	}
	if err := request.readBody(reader); err != nil {
		writeError(connection, http.StatusBadRequest, err.Error())
		return
	}

	startedAt := time.Now()
	response, err := forward(server.clients, request, metadata)
	if err != nil {
		server.logger.Printf("%s %s: %v", request.Method, metadata.Host, err)
		server.logActivity(requestEvent{
			Event:      "request",
			ID:         metadata.Trace,
			Outcome:    "failed",
			DurationMS: time.Since(startedAt).Milliseconds(),
			Error:      truncateActivityError(err.Error()),
		})
		writeError(connection, http.StatusBadGateway, err.Error())
		return
	}

	if err := writeResponse(connection, request.Method, response); err != nil {
		server.logger.Printf("write response: %v", err)
		server.logActivity(requestEvent{
			Event:      "request",
			ID:         metadata.Trace,
			Outcome:    "failed",
			DurationMS: time.Since(startedAt).Milliseconds(),
			Error:      truncateActivityError("write response: " + err.Error()),
		})
		return
	}

	server.logActivity(requestEvent{
		Event:      "request",
		ID:         metadata.Trace,
		Outcome:    "succeeded",
		StatusCode: response.StatusCode,
		Protocol:   response.Proto,
		DurationMS: time.Since(startedAt).Milliseconds(),
	})
}

func (server *Server) logActivity(event requestEvent) {
	if server.activityLogger == nil {
		return
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		server.logger.Printf("encode activity event: %v", err)
		return
	}
	server.activityLogger.Print(string(encoded))
}

func truncateActivityError(message string) string {
	if len(message) <= maxActivityErrorBytes {
		return message
	}
	return message[:maxActivityErrorBytes-3] + "..."
}
