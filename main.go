package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Settings struct {
	HeavyModel string
	OllamaUrl  string
	SearxNGUrl string
	LightModel string
}

type OperatingMode int

const (
	Research OperatingMode = iota
	Normal
	Search
	Code
	FastCode
)

func memoryHandler(state *State, command string) {
	if command == "l" {
		fmt.Println("Listing memories")
		listMemories(state.Database)
	}

	if command[:1] == "u" {
		fmt.Println("Using memory")
		memoryId := strings.TrimSpace(command[1:])
		loadMemory(state, memoryId)
	}

	if command == "n" {
		fmt.Println("Creating a new memory")
		saveMemory(state)
		forgetMemory(state)
		rememberMemory(state)

	}

	if command == "r" {
		fmt.Println("Resuming last memory")
		saveMemory(state)
		resumeLastMemory(state)
	}
	if command == "rf" {
		fmt.Println("Resuming last memory and not remembering this session")
		resumeLastMemory(state)
	}

	if command == "nf" {
		fmt.Println("Creating a new memory and not remembering this session")
		forgetMemory(state)
		rememberMemory(state)
	}

	if command[:1] == "d" {
		fmt.Println("Deleting memory")
		memoryId := strings.TrimSpace(command[1:])
		deleteMemory(state, memoryId)

	}

	if command == "h" {
		fmt.Println("Memory handler help")
		fmt.Println("This way you can handle your program's memories")
		fmt.Println("Usage:")
		fmt.Println("  /memory <Flag> <Flag Value>")
		fmt.Println("Flags:")
		fmt.Println("  l - list memories")
		fmt.Println("  u <Memory Id> - Load a specific memory")
		fmt.Println("  r - Resume last memory")
		fmt.Println("  rf - Rusume last memory and don't save the old one")
		fmt.Println("  n - create a new memory and save the old one")
		fmt.Println("  nf - create a new memory and don't save the old one")
	}
}
func modeHandler(state *State, command string) {
	if command == "r" {
		fmt.Println("Switched to research mode")
		state.OperatingMode = Research
	}
	if command == "s" {
		fmt.Println("Switched to search mode")
		state.OperatingMode = Search
	}
	if command == "n" {
		fmt.Println("Switched to normal mode")
		state.OperatingMode = Normal
	}
	if command == "c" {
		fmt.Println("Switched to code mode")
		state.OperatingMode = Code
	}
	if command == "fc" {
		fmt.Println("Switched to fast code mode")
		state.OperatingMode = FastCode
	}
	if command == "h" {
		fmt.Println("Mode handler help")
		fmt.Println("This is the way you can decide what modes your llm works in")
		fmt.Println("Usage:")
		fmt.Println("  /mode <Flag>")
		fmt.Println("flags:")
		fmt.Println("  r - research mode (use if you want to have the model deep dive)")
		fmt.Println("  s - search mode (use if you want the model to quickly search the web for current information)")
		fmt.Println("  n - normal mode (use if you want the model to reply by itself)")
		fmt.Println("  c - code mode (Use for accurate code examples with explanations)")
		fmt.Println("  fc - fast code mode (Use for quick code prototyping)")
	}
}

func commandHandler(state *State, command string) {
	parsedCommand := strings.Split(command, " ")
	commandName := parsedCommand[0]
	switch commandName {
	case "exit":
		saveMemory(state)
		os.Exit(0)
	case "current":
		fmt.Println(state.Memory.Title)
	case "mode":
		modeHandler(state, strings.Join(parsedCommand[1:], " "))
	case "memory":
		memoryHandler(state, strings.Join(parsedCommand[1:], " "))
	case "help":
		fmt.Println("YAAP - Yet Another Ai Program")
		fmt.Println("commands:")
		fmt.Println("  /mode: change the execution mode (/mode h) for help")
		fmt.Println("  /memory: memory commands (/memory h) for help")
		fmt.Println("  /current: look at the name of the current loaded memory")
		fmt.Println("  /exit: exit the program")
	default:
		fmt.Println("Couldn't find command")
	}

}

func executePrompt(state *State, prompt string) FinalAnswer {
	var finalAnswer string
	var sources []string
	switch state.OperatingMode {
	case Research:
		fmt.Println("Researching!")
		finalAnswer, sources = researchMode(state, prompt)
	case Search:
		fmt.Println("Looking it up!")
		finalAnswer = lookupMode(state, prompt)
	case Normal:
		fmt.Println("Answering from memory!")
		answer := callHeavyLLM(
			state.Settings,
			buildPrompt(prompt, "", state.Memory),
			`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
		)
		fmt.Printf("\nToken Count: %d", answer.PromptEvalCount)
		finalAnswer = answer.Response
	case Code:
		fmt.Println("Coding!")
		finalAnswer, sources = codeMode(state, prompt)
	case FastCode:
		fmt.Println("Fast Coding!")
		finalAnswer, sources = lightCodeMode(state, prompt)

	}
	return FinalAnswer{finalAnswer, sources}
}

type FinalAnswer struct {
	FinalAnswer string
	Sources     []string
}

func elapsedTime(resultChan chan FinalAnswer, ticker *time.Ticker, start time.Time) FinalAnswer {
	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(start)
			if elapsed.Seconds() > 0.3 {
				fmt.Printf("\x1b[?2K")
				fmt.Printf("\r")
				fmt.Printf("Elapsed: %s", elapsed.Round(time.Second))

			}

		case answer := <-resultChan:
			return answer
		}
	}
}

func main() {
	searxUrl := flag.String(
		"searx-url",
		getenv("SEARXNG_URL", "http://localhost:8080"),
		"SearxNG server address",
	)
	heavyModel := flag.String(
		"heavy-model",
		getenv("HEAVY_MODEL", "qwen-40k"),
		"The name of the model that will run inference",
	)
	lightModel := flag.String(
		"light-model",
		getenv("LIGHT_MODEL", "gemma-128k"),
		"The name of the model that will run inference",
	)
	ollamaUrl := flag.String(
		"ollama-url",
		getenv("OLLAMA_URL", "http://localhost:11434"),
		"The link to the ollama server",
	)
	shouldListMemories := flag.Bool(
		"list-memories",
		false,
		"Should list memories",
	)
	memoryToDelete := flag.String(
		"delete-memory",
		"",
		"Should list memories",
	)
	resume := flag.Bool(
		"resume",
		false,
		"Should resume last session",
	)
	memoryToLoad := flag.String(
		"load-memory",
		"",
		"Memory id of memory to load from the list of memories. (--list-memories to see memories)",
	)
	db := initDb()
	defer db.Close()
	flag.Parse()
	settings := Settings{
		HeavyModel: *heavyModel,
		LightModel: *lightModel,
		OllamaUrl:  *ollamaUrl,
		SearxNGUrl: *searxUrl,
	}
	if *shouldListMemories {
		listMemories(db)
		return
	}
	logFile, err := os.OpenFile(".YAAP.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic("failed to open log file: " + err.Error())
	}
	state := NewState(settings, db, logFile)
	state.Logger.Info("Run started")
	if *memoryToDelete != "" {
		deleteMemory(state, *memoryToDelete)
		return
	}
	if *memoryToLoad != "" {
		loadMemory(state, *memoryToLoad)
	}

	if *resume {
		resumeLastMemory(state)
	}
	resultChan := make(chan FinalAnswer)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		fmt.Print("> ")
		reader := bufio.NewReader(os.Stdin)

		prompt, err := reader.ReadString('\n')
		if err != nil {
			state.Logger.Error("Couldn't read user input", slog.Any("err", err))
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		if prompt[0] == '/' {
			commandHandler(state, prompt[1:])
			continue
		}

		start := time.Now()
		go func() {
			resultChan <- executePrompt(state, prompt)
		}()
		answer := elapsedTime(resultChan, ticker, start)
		if state.Remember {
			if len(state.Memory.Interactions) == 0 {
				state.Memory.Title = prompt
				state.Memory.Id = uuid.New().String()
			}
			state.Memory.Interactions = append(state.Memory.Interactions, ChatInteraction{Question: prompt, Answer: answer.FinalAnswer, Links: answer.Sources})
		}

		out, err := state.Renderer.Render(answer.FinalAnswer)
		if err != nil {
			fmt.Println(answer.FinalAnswer)
		} else {
			fmt.Println(out)
		}
		fmt.Println(strings.Join(answer.Sources, "\n"))
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

//TODO: jump to top of response on response
//TODO: enable memory exporting
//TODO: auto-complete for inline commands
//TODO: enable multiline prompts
