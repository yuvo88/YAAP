package main

import (
	"database/sql"
	"log/slog"
	"os"

	"github.com/charmbracelet/glamour"
)

type State struct {
	Settings      Settings
	OperatingMode OperatingMode
	Memory        Memory
	Remember      bool
	Database      *sql.DB
	Renderer      *glamour.TermRenderer
	Logger        *slog.Logger
}

func NewState(settings Settings, database *sql.DB, logFile *os.File) *State {
	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(-1),
	)

	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	}))

	return &State{
		Settings:      settings,
		OperatingMode: Search,
		Remember:      true,
		Database:      database,
		Renderer:      r,
		Logger:        logger,
	}
}
