package server

import (
	"time"

	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/settings"
)

// hostSamplePolicy reads the host-sampling configuration.
//
// It is a function rather than a value so the sampler re-reads it on every tick: an
// operator turning sampling off, or changing the interval, should not have to restart
// the process to be obeyed.
func (s *Server) hostSamplePolicy() monitoring.SamplePolicy {
	policy := monitoring.DefaultSamplePolicy()

	if s.settings == nil {
		return policy
	}

	reg := settings.Admin()
	policy.Enabled = s.settings.BoolGet(reg, "monitoring", "hostEnabled")

	if seconds := s.settings.IntGet(reg, "monitoring", "hostIntervalSeconds"); seconds > 0 {
		policy.Interval = time.Duration(seconds) * time.Second
	}
	policy.RetentionDays = s.settings.IntGet(reg, "monitoring", "hostRetentionDays")

	return policy
}

// StartHostSampler begins recording this machine's resource use.
//
// It returns the stop function, following the scheduler's shape so main can shut both
// down the same way.
func (s *Server) StartHostSampler() func() {
	sampler := monitoring.NewSampler(s.app, monitoring.NewHostReader(s.cfg.DataDir), s.hostSamplePolicy)
	sampler.Start()
	s.sampler = sampler

	return sampler.Stop
}
