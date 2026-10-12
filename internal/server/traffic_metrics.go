package server

import (
	"time"

	"github.com/gin-gonic/gin"
)

// trafficMetrics records every request for the monitoring panel.
//
// It sits above the routers so it sees the health endpoints and the API as well as
// the panel: an error rate that excluded the health checks would be a rate that
// disagreed with the load balancer.
func (s *Server) trafficMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()

		c.Next()

		// Recorded after the handler so the status is known, and recorded even when
		// the response was aborted, because an aborted request is exactly the kind
		// of thing the traffic panel exists to show.
		s.metrics.Observe(c.Writer.Status(), time.Since(started))
	}
}
