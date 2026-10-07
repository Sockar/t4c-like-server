package network

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sockar/t4c-like-server/internal/game"
	"github.com/Sockar/t4c-like-server/internal/protocol"
	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
)

type Server struct {
	world    *game.World
	upgrader websocket.Upgrader
	nextID   atomic.Uint64
	clients  sync.Map
}

func NewServer(world *game.World) *Server {
	return &Server{
		world: world,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/ws" {
		http.NotFound(writer, request)
		return
	}
	connection, err := s.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}

	client := &peer{
		key:    s.nextID.Add(1),
		conn:   connection,
		sendCh: make(chan protocol.Message, 64),
		done:   make(chan struct{}),
	}
	s.clients.Store(client.Key(), client)
	go client.writePump()
	defer client.close()
	defer s.clients.Delete(client.Key())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.world.Submit(ctx, game.Event{Client: client, Type: "disconnect"}); err != nil {
			log.Printf("submit disconnect for client %s: %v", client.Key(), err)
		}
	}()
	client.readPump(request, s.world)
}

func (s *Server) Close() {
	s.clients.Range(func(_, value any) bool {
		value.(*peer).close()
		return true
	})
}

type peer struct {
	key       uint64
	conn      *websocket.Conn
	sendCh    chan protocol.Message
	done      chan struct{}
	closeOnce sync.Once
}

func (p *peer) Key() string {
	return strconv.FormatUint(p.key, 10)
}

func (p *peer) Send(message protocol.Message) bool {
	select {
	case <-p.done:
		return false
	case p.sendCh <- message:
		return true
	default:
		return false
	}
}

func (p *peer) close() {
	p.closeOnce.Do(func() {
		close(p.done)
		_ = p.conn.Close()
	})
}

func (p *peer) readPump(request *http.Request, world *game.World) {
	defer p.close()
	p.conn.SetReadLimit(maxMessageSize)
	_ = p.conn.SetReadDeadline(time.Now().Add(pongWait))
	p.conn.SetPongHandler(func(string) error {
		return p.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		message, err := protocol.Decode(data)
		if err != nil {
			p.Send(protocol.Message{Type: "error", Payload: map[string]string{
				"code": "invalid_message", "message": err.Error(),
			}})
			continue
		}
		if err := world.Submit(request.Context(), game.Event{
			Client: p, Type: message.Type, Payload: message.Payload,
		}); err != nil {
			return
		}
	}
}

func (p *peer) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer p.close()

	for {
		select {
		case <-p.done:
			return
		case message := <-p.sendCh:
			_ = p.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := p.conn.WriteJSON(message); err != nil {
				return
			}
		case <-ticker.C:
			_ = p.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := p.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
