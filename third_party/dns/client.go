package dns

import "time"

const (
        dnsTimeout     time.Duration = 2 * time.Second
        tcpIdleTimeout time.Duration = 8 * time.Second
)
