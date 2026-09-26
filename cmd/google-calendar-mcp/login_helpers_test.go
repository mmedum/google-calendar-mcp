package main

import (
	"errors"

	"github.com/zalando/go-keyring"

	"github.com/mmedum/google-calendar-mcp/v2/internal/config"
	"github.com/mmedum/google-calendar-mcp/v2/internal/credentials"
)

func keyringNotFound() error { return keyring.ErrNotFound }

func configForTest() config.Config {
	return config.Config{Profile: "default", Sharing: true, HTTPTimeout: 0}
}

func errorIsNotFound(err error) bool {
	return errors.Is(err, credentials.ErrNotFound) || errors.Is(err, credentials.ErrKeyringSilent)
}
