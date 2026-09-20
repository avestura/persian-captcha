package store

import (
	"context"
	"fmt"

	"avestura.dev/persian-captcha/internal/config"
)

// New builds the Store selected by the configuration.
func New(ctx context.Context, cfg config.StoreConfig) (Store, error) {
	switch cfg.Driver {
	case "", "memory":
		return NewMemory(), nil
	case "redis":
		return NewRedis(ctx, RedisOptions{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			Prefix:   cfg.Redis.Prefix,
		})
	default:
		return nil, fmt.Errorf("store: unknown driver %q (want memory or redis)", cfg.Driver)
	}
}
