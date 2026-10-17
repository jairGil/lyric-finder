package storage

import "lyrics/internal/ports"

var _ ports.Persistence = (*Store)(nil)
