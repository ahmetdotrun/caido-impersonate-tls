package relay

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bogdanfinn/fhttp/httptrace"
)

const maxConcurrentConnections = 128
const maxActivityErrorBytes = 500

type serverLimits struct {
	headerTimeout time.Duration
	idleTimeout   time.Duration
	queueTimeout  time.Duration
}

type requestEvent struct {
	Event      string `json:"event"`
	ID         string `json:"id"`
	Outcome    string `json:"outcome"`
	StatusCode int    `json:"statusCode,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
	Warning    string `json:"warning,omitempty"`
}

type Server struct {
	token          string
	logger         *log.Logger
	activityLogger *log.Logger
	clients        *clientPool
	slots          chan struct{}
	websocketSlots chan struct{}
	queued         chan struct{}
	limits         serverLimits
}

func NewServer(token string, logger *log.Logger, activityLoggers ...*log.Logger) *Server {
	server := &Server{
		token:          token,
		logger:         logger,
		clients:        newClientPool(),
		slots:          make(chan struct{}, maxConcurrentConnections),
		websocketSlots: make(chan struct{}, maxConcurrentConnections),
		queued:         make(chan struct{}, maxConcurrentConnections),
		limits:         serverLimits{headerTimeout: 60 * time.Second, idleTimeout: 5 * time.Minute, queueTimeout: 10 * time.Second},
	}
	if len(activityLoggers) > 0 {
		server.activityLogger = activityLoggers[0]
	}
	return server
}

func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	stopListener := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer func() {
		cancel()
		_ = listener.Close()
		stopListener()
		workers.Wait()
	}()
	// Bound header readers as well as authenticated work and the wait queue.
	accepted := make(chan struct{}, cap(server.slots)+cap(server.websocketSlots)+cap(server.queued))

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case accepted <- struct{}{}:
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-accepted }()
				server.handleContext(ctx, connection)
			}()
		default:
			_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
			writeError(connection, http.StatusServiceUnavailable, "transport connection capacity exhausted")
			server.logger.Print("transport connection capacity exhausted")
			_ = connection.Close()
		}
	}
}

func (server *Server) handle(connection net.Conn) {
	server.handleContext(context.Background(), connection)
}

func (server *Server) handleContext(parent context.Context, connection net.Conn) {
	defer connection.Close()
	stopShutdown := context.AfterFunc(parent, func() { _ = connection.Close() })
	defer stopShutdown()
	_ = connection.SetDeadline(time.Now().Add(server.limits.headerTimeout))

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
	if err := request.prepareBody(reader); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(connection, status, err.Error())
		server.logActivity(requestEvent{Event: "request", ID: metadata.Trace, Outcome: "failed", Error: err.Error()})
		return
	}
	_ = connection.SetDeadline(time.Time{})
	writer := idleWriter{Conn: connection, timeout: server.limits.idleTimeout}
	warning := profileCoherenceWarning(metadata.Profile, request.firstHeader("User-Agent"))

	startedAt := time.Now()
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(context.Canceled)
	stopRead := context.AfterFunc(ctx, func() { _ = connection.SetReadDeadline(time.Now()) })
	defer stopRead()
	websocket := request.isWebSocketUpgrade()
	slots := server.slots
	if websocket {
		slots = server.websocketSlots
	}
	if err := server.acquire(ctx, slots); err != nil {
		writeError(writer, http.StatusServiceUnavailable, err.Error())
		server.logActivity(requestEvent{Event: "request", ID: metadata.Trace, Outcome: "failed", DurationMS: time.Since(startedAt).Milliseconds(), Error: err.Error(), Warning: warning})
		return
	}
	defer func() { <-slots }()

	watch := newActivityDeadline(cancel)
	defer watch.stop()
	watch.reset(server.limits.headerTimeout)
	bodyDone := make(chan struct{})
	if request.Body == nil {
		close(bodyDone)
	} else {
		request.Body = &progressReader{reader: request.Body, watch: watch, timeout: server.limits.idleTimeout, remaining: request.BodyLength, done: bodyDone}
	}
	if !websocket {
		// One request owns this connection. Once its upload is consumed, EOF
		// means Caido has cancelled it; propagate that to the upstream stream.
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-bodyDone:
			}
			_, _ = io.Copy(io.Discard, reader)
			cancel(context.Canceled)
		}()
	}
	requestContext := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { watch.reset(server.limits.headerTimeout) },
	})
	response, err := forwardContext(requestContext, server.clients, request, metadata)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		if errors.Is(context.Cause(ctx), errRelayTimeout) {
			err = errRelayTimeout
			status = http.StatusGatewayTimeout
		}
		server.logger.Printf("%s %s: %v", request.Method, metadata.Host, err)
		server.logActivity(requestEvent{
			Event:      "request",
			ID:         metadata.Trace,
			Outcome:    "failed",
			DurationMS: time.Since(startedAt).Milliseconds(),
			Error:      truncateActivityError(err.Error()),
			Warning:    warning,
		})
		writeError(writer, status, err.Error())
		return
	}
	if request.isWebSocketUpgrade() && response.StatusCode == http.StatusSwitchingProtocols {
		watch.stop()
		upstream, ok := response.Body.(io.ReadWriteCloser)
		if !ok {
			_ = response.Body.Close()
			message := "upstream protocol switch did not provide a bidirectional connection"
			server.logger.Printf("%s %s: %s", request.Method, metadata.Host, message)
			server.logActivity(requestEvent{
				Event:      "request",
				ID:         metadata.Trace,
				Outcome:    "failed",
				DurationMS: time.Since(startedAt).Milliseconds(),
				Error:      message,
				Warning:    warning,
			})
			writeError(writer, http.StatusBadGateway, message)
			return
		}
		if err := writeSwitchingProtocols(writer, response); err != nil {
			_ = upstream.Close()
			server.logger.Printf("write websocket response: %v", err)
			server.logActivity(requestEvent{
				Event:      "request",
				ID:         metadata.Trace,
				Outcome:    "failed",
				DurationMS: time.Since(startedAt).Milliseconds(),
				Error:      truncateActivityError("write response: " + err.Error()),
				Warning:    warning,
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
			Warning:    warning,
		})
		_ = connection.SetDeadline(time.Time{})
		if err := bridgeProtocolSwitch(connection, reader, upstream); err != nil {
			server.logger.Printf("websocket relay %s: %v", metadata.Host, err)
		}
		return
	}

	watch.reset(server.limits.idleTimeout)
	response.Body = &progressBody{progressReader: &progressReader{reader: response.Body, watch: watch, timeout: server.limits.idleTimeout, remaining: -1}, Closer: response.Body}
	if err := writeResponse(writer, request.Method, response); err != nil {
		if errors.Is(context.Cause(ctx), errRelayTimeout) {
			err = errRelayTimeout
		}
		server.logger.Printf("write response: %v", err)
		server.logActivity(requestEvent{
			Event:      "request",
			ID:         metadata.Trace,
			Outcome:    "failed",
			DurationMS: time.Since(startedAt).Milliseconds(),
			Error:      truncateActivityError("write response: " + err.Error()),
			Warning:    warning,
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
		Warning:    warning,
	})
}

func (server *Server) acquire(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		return nil
	default:
	}
	select {
	case server.queued <- struct{}{}:
		defer func() { <-server.queued }()
	default:
		return errors.New("transport wait queue is full")
	}
	timer := time.NewTimer(server.limits.queueTimeout)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return nil
	case <-timer.C:
		return errors.New("transport capacity wait timed out")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func bridgeProtocolSwitch(
	downstream net.Conn,
	downstreamReader io.Reader,
	upstream io.ReadWriteCloser,
) error {
	results := make(chan error, 2)
	go func() {
		_, err := io.Copy(upstream, downstreamReader)
		results <- err
	}()
	go func() {
		_, err := io.Copy(downstream, upstream)
		results <- err
	}()

	first := <-results
	_ = upstream.Close()
	_ = downstream.Close()
	second := <-results
	if first != nil && !errors.Is(first, net.ErrClosed) {
		return first
	}
	if second != nil && !errors.Is(second, net.ErrClosed) {
		return second
	}
	return nil
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
