package main

import (
	"database/sql"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

func saveMemory(state *State) {
	if len(state.Memory.Interactions) == 0 {
		return
	}
	if state.Memory.Id == "" {
		state.Memory.Id = uuid.New().String()
	}
	dir := memories_directory_name
	file_path := filepath.Join(dir, state.Memory.Id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(err)
	}

	file, err := os.Create(file_path)
	if err != nil {
		panic(err)
	}

	defer file.Close()

	encoder := gob.NewEncoder(file)

	if err := encoder.Encode(state.Memory); err != nil {
		panic(err)
	}
	_, err = state.Database.Exec(
		`INSERT INTO memories (id, title, updated) 
		 VALUES (?, ?, ?)
		 ON CONFLICT (id) DO UPDATE
		 SET 
			updated = excluded.updated`,
		 state.Memory.Id, state.Memory.Title, time.Now().Unix())
	if err != nil {
		panic(err)
	}
}
func deleteMemory(state *State, memory_id string) {
	file_path := filepath.Join(memories_directory_name, memory_id)
	_, err := state.Database.Exec("DELETE FROM memories WHERE id=?", memory_id)
	if err != nil {
		panic("Couldn't delete memory from database")
	}
	err = os.Remove(file_path)
	if err != nil {
		panic("Couldn't remove memory from disk")
	}
}
func forgetMemory(state *State) {
	state.Remember = false
	state.Memory.Interactions = []ChatInteraction{}
	state.Memory.Title = ""
}
func rememberMemory(state *State) {
	state.Remember = true
}

type MemoryDto struct {
	Id      string
	Title   string
	Updated string
}
func resumeLastMemory(state *State) {
	row := state.Database.QueryRow("SELECT id FROM memories ORDER BY updated DESC LIMIT 1")

	var memoryId string
	err := row.Scan(&memoryId)
	if err != nil {
		panic("Couldn't find last memory")
	}
	
	file_path := filepath.Join(memories_directory_name, memoryId)
	file, _ := os.Open(file_path)
	defer file.Close()

	decoder := gob.NewDecoder(file)

	var memory Memory

	err = decoder.Decode(&memory)
	if err != nil {
		fmt.Println("Memory not found")
	}
	state.Memory = memory
	state.Memory.PrintMemory(state.Renderer)


}
func listMemories(database *sql.DB) {
	rows, err := database.Query("SELECT * FROM memories ORDER BY updated")

	if err != nil {
		panic("")
	}
	defer rows.Close()
	var memories []MemoryDto
	for rows.Next() {
		var memory MemoryDto
		if err := rows.Scan(&memory.Id, &memory.Title, &memory.Updated); err != nil {
			panic("")
		}

		memories = append(memories, memory)

	}

	if err := rows.Err(); err != nil {
		panic("")
	}
	for _, memory := range memories {
		fmt.Printf("%s | %s | %s\n", memory.Id, memory.Title, memory.Updated)
	}

}

func loadMemory(state *State, memory_id string) {

	file_path := filepath.Join(memories_directory_name, memory_id)
	file, _ := os.Open(file_path)
	defer file.Close()

	decoder := gob.NewDecoder(file)

	var memory Memory

	err := decoder.Decode(&memory)
	if err != nil {
		fmt.Println("Memory not found")
	}
	state.Memory = memory
	state.Memory.PrintMemory(state.Renderer)
}

func initDb() *sql.DB {
	db, err := sql.Open("sqlite3", memories_db_name)
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

