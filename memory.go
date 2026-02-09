package main

import (
	"database/sql"
	"encoding/gob"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const memoriesDbName string = ".memories.db"
const memoriesDirectoryName string = ".memories"

func saveMemory(state *State) {
	state.Logger.Debug("Saving current memory", slog.String("memory_id", state.Memory.Id))
	if len(state.Memory.Interactions) == 0 {
		return
	}
	if state.Memory.Id == "" {
		state.Memory.Id = uuid.New().String()
	}
	dir := memoriesDirectoryName
	filePath := filepath.Join(dir, state.Memory.Id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		state.Logger.Error("Failed to make memory directory", slog.Any("err", err))
	}

	file, err := os.Create(filePath)
	if err != nil {
		state.Logger.Error("Failed to save memory to directory", slog.Any("err", err))
	}

	defer file.Close()

	encoder := gob.NewEncoder(file)

	if err := encoder.Encode(state.Memory); err != nil {
		state.Logger.Error("Failed to encode memory struct", slog.Any("err", err))
	}
	_, err = state.Database.Exec(
		`INSERT INTO memories (id, title, updated) 
		 VALUES (?, ?, ?)
		 ON CONFLICT (id) DO UPDATE
		 SET 
			updated = excluded.updated`,
		state.Memory.Id, state.Memory.Title, time.Now().Unix())
	if err != nil {
		state.Logger.Error("Failed to insert memory into db", slog.Any("err", err))
	}
}
func deleteMemory(state *State, memoryId string) {
	state.Logger.Debug("Deleting memory", slog.String("memory_id", memoryId))
	filePath := filepath.Join(memoriesDirectoryName, memoryId)
	_, err := state.Database.Exec("DELETE FROM memories WHERE id=?", memoryId)
	if err != nil {
		state.Logger.Error("Failed to delete memory from DB", slog.Any("err", err))
	}
	err = os.Remove(filePath)
	if err != nil {
		state.Logger.Error("Failed to delete memory from disk", slog.Any("err", err))
	}
}
func forgetMemory(state *State) {
	state.Logger.Debug("Forgetting last memory")
	state.Remember = false
	state.Memory.Interactions = []ChatInteraction{}
	state.Memory.Title = ""
}
func rememberMemory(state *State) {
	state.Logger.Debug("Remembering chat")
	state.Remember = true
}

type MemoryDto struct {
	Id      string
	Title   string
	Updated int64
}

func resumeLastMemory(state *State) string {
	state.Logger.Debug("Resuming last memory")
	row := state.Database.QueryRow("SELECT id FROM memories ORDER BY updated DESC LIMIT 1")

	var memoryId string
	err := row.Scan(&memoryId)
	if err != nil {
		state.Logger.Error("Last memory wasn't found in the database", slog.Any("err", err))
	}

	filePath := filepath.Join(memoriesDirectoryName, memoryId)
	file, _ := os.Open(filePath)
	defer file.Close()

	decoder := gob.NewDecoder(file)

	var memory Memory

	err = decoder.Decode(&memory)
	if err != nil {
		state.Logger.Error("Last memory wasn't found on the disk", slog.Any("err", err))
	}
	state.Memory = memory
	return state.Memory.GetPrintedMemory(state.Renderer)

}
func listMemories(database *sql.DB) string {
	rows, err := database.Query("SELECT * FROM memories ORDER BY updated")

	if err != nil {
		panic(fmt.Sprintf("Failed to list memories in DB, err: %s", err))
	}
	defer rows.Close()
	var memories []MemoryDto
	for rows.Next() {
		var memory MemoryDto
		if err := rows.Scan(&memory.Id, &memory.Title, &memory.Updated); err != nil {
			panic(fmt.Sprintf("Failed to retreive memories from result, err: %s", err))
		}

		memories = append(memories, memory)

	}

	if err := rows.Err(); err != nil {
		panic(fmt.Sprintf("Empty result from database, err: %s", err))
	}
	var memoriesString strings.Builder
	fmt.Fprintf(&memoriesString, "Memories\n\n")
	for _, memory := range memories {
		t := time.Unix(memory.Updated, 0).In(time.Local)
		title := strings.ReplaceAll(memory.Title, "\n", "\\n")
		if len(title) > 100 {
			title = title[:100]
		}
		fmt.Fprintf(&memoriesString, "%s | %s | %s\n\n", memory.Id, title, t.Format("2006-01-02 15:04:05"))
	}
	return memoriesString.String()

}

func loadMemory(state *State, memoryId string) string{
	state.Logger.Debug("Loading memory", slog.String("memory_id", memoryId))
	filePath := filepath.Join(memoriesDirectoryName, memoryId)
	file, _ := os.Open(filePath)
	defer file.Close()

	decoder := gob.NewDecoder(file)

	var memory Memory

	err := decoder.Decode(&memory)
	if err != nil {
		state.Logger.Warn("Memory not found", slog.String("memory_id", memoryId))
	}
	state.Memory = memory
	return state.Memory.GetPrintedMemory(state.Renderer)
}

func initDb() *sql.DB {
	db, err := sql.Open("sqlite3", memoriesDbName)
	if err != nil {
		panic(err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			updated INTEGER NOT NULL
		)`,
	)

	if err != nil {

		panic(err)

	}

	return db
}
