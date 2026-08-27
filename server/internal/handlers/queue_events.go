package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
	"net/http"
	"time"
)

func (a *API) QueueEvents(c *gin.Context) {
	if a.queueEvents == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	allowed := map[string]struct{}{}
	for _, origin := range a.config.AllowedOrigins() {
		allowed[origin] = struct{}{}
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(request *http.Request) bool { _, ok := allowed[request.Header.Get("Origin")]; return ok }}
	actor, _ := middleware.ActorFrom(c)
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer func() { _ = connection.Close() }()
	events, unsubscribe := a.queueEvents.Subscribe(actor)
	defer unsubscribe()
	for event := range events {
		_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := connection.WriteJSON(services.JobEventEnvelope{Type: event.Type, Data: event}); err != nil {
			return
		}
	}
}
