package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func buildPrompt(state *State, prompt string, context string) string {
	if state.FileName == "" {
		return fmt.Sprintf(`
			[context]
			%s
			[history]
			%s
			[question]
			%s
		`, context, state.Memory.GetMemoryForModel(), prompt)
	}
	fileContent, err := os.ReadFile(state.FileName)
	if err != nil {
		state.Logger.Warn("Failed to read file", slog.String("fileName", state.FileName), slog.Any("err", err))
		return fmt.Sprintf(`
			[context]
			%s
			[history]
			%s
			[question]
			%s
			`, context, state.Memory.GetMemoryForModel(), prompt) // TODO: Return err
	}


	return fmt.Sprintf(`
		[context]
		%s
		[file]
		%s
		[history]
		%s
		[question]
		%s
	`, context, string(fileContent), state.Memory.GetMemoryForModel(), prompt)

}
func getLinks(state *State, client *http.Client, question string) []string {
	state.Logger.Debug("Getting links")
	month := time.Now().Month().String()
	year := time.Now().Year()

	queriesList := getQueriesFromLightLLM(
		state,
		buildPrompt(state, question, ""),
		fmt.Sprintf(`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google queries
		- The current date is %s %d if the user asks about something happening now
		- You reply with between 1 and 3 short google queries separated by a newline character
		- If the question references a file look at [file]
		- Each query is a sentence built of multiple words
		- **NEVER** have a query with only one word
		- Keep the queries short ( between 3 to 5 words )
		`, month, year)).Queries

	var queries strings.Builder

	for _, query := range queriesList {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		result, _ := searxSearch(client, state.Settings.SearxNGUrl, query, 1)
		fmt.Fprintf(&queries, "%s", result)
	}

	links := getLinksFromLightLLM(
		state,
		buildPrompt(state, question, queries.String()),
		fmt.Sprintf(
			`You answer quickly and accurately using the provided markdown web snippets.
			Rules:
			- Use the provided markdown web snippets and only the provided markdown web snippets as context
			- If the question references a file look at [file]
			- Respond with 1-3 links that the most relavant to the users question and closest to %s %d
			- Please make sure that you cover all parts of the user's question with the links you provide
			- Only return links separated by newline characters nothing else
			`, month, year,
		),
	)

	linkList := make(map[string]struct{})
	for _, link := range links.Links {
		trimmed := strings.TrimSpace(link)
		if trimmed == "" {
			continue
		}
		result := getLinksFromLightLLM(
			state,
			buildPrompt(state, question, getRequest(client, link)),
			fmt.Sprintf(
				`You answer quickly and accurately using the provided markdown web snippets.
			Rules:
			- Use the provided markdown web snippets and only the provided markdown web snippets as context
			- If the question references a file look at [file]
			- Respond with 1-3 links that the most relavant to the users question and closest to %s %d
			- Please make sure that you cover all parts of the user's question with the links you provide
			- Only return links separated by newline characters nothing else
			`, month, year,
			),
		)
		for _, l := range result.Links {
			linkList[l] = struct{}{}
		}
		// fmt.Fprintf(&sb, "# Original link: %s\n# Summarized Page:\n%s\n\n", trimmed, result)
	}
	items := make([]string, 0, len(linkList))
	for k := range linkList {
		items = append(items, k)
	}
	return items

}
func researchMode(state *State, question string) (string, []string) {
	state.Logger.Debug("Triggering research mode")
	client := &http.Client{}
	links := getLinks(state, client, question)

	var toParse strings.Builder
	state.Logger.Debug("Parsing links")
	for _, article := range links {
		result := callLightLLM(
			state,
			fmt.Sprintf(`
			[web page]
			%s
			[question]
			%s	
			[prompt]
			Please get relavant information for the question from the web page 
			`, getRequest(client, article), question),
			`You get relavant to a question from a web page
			Rules:
			- Always return a summary of only information relavant to the user question
			- Please make sure that you return the whole context for the question
			- **NEVER** respond with anything that is not code
			`,
		)
		// result := getRequest(client, item)
		fmt.Fprintf(&toParse, "%s", result.Response)
	}
	state.Logger.Debug("Preparing final response")
	finalAnswer := callHeavyLLM(
		state,
		buildPrompt(state, question, toParse.String()),
		`You answer quickly and accurately using the provided markdown web pages.
		Rules:
		- Please **always provide a link** to the web page that you got your information from.
		- Please **always cite your sources**
		- Use the provided links and fetched pages as context
		- If you don't understand the context of the user's question look for it in the history section
		- If the question references a file look at [file]
		- If you see information not relavant to the question in the page please disregard it
		- Only reply to the user's question
		- Respond with information closest to %s %d
		- Look for dates in provided pages and specify it in your response
		- Please provide the exact answer for the user's question according to the markdown web pages provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided pages please say so explicitly.
		- You always respond in markdown
		`,
	)

	fmt.Printf("\nToken count: %d\n", finalAnswer.PromptEvalCount)

	return finalAnswer.Response, links
}

func codeMode(state *State, question string) (string, []string) {
	state.Logger.Debug("Triggering code mode")
	client := &http.Client{}

	links := getLinks(state, client, question)

	var toParse strings.Builder
	for _, link := range links {
		result := callLightLLM(
			state,
			fmt.Sprintf(`
			[web page]
			%s
			[question]
			%s	
			[prompt]
			Please get relavant code example from this web page
			`, getRequest(client, link), question),
			`You get code examples from web pages
			Rules:
			- Always only return code examples
			- Return code examples relavant to the question
			- **NEVER** respond with anything that is not code
			`,
		)
		// result := getRequest(client, item)
		fmt.Fprintf(&toParse, "%s", result.Response)
	}
	finalAnswer := callHeavyLLM(
		state,
		buildPrompt(state, question, toParse.String()),
		`You answer quickly and accurately using the provided code examples.
		Rules:
		- Please **always provide a link** to the web page that you got your information from.
		- Please **always cite your sources**
		- If you don't understand the context of the user's question look for it in the history section
		- Use the provided code examples as context for your answer
		- If the question references a file look at [file]
		- If you see information or code not relavant to the question in the page please disregard it
		- Only reply to the user's question
		- Respond with information closest to %s %d
		- Look for dates in provided pages and specify it in your response
		- Please provide the exact example for the user's question according to the code examples provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided examples please say so explicitly.
		- You always respond in markdown
		`,
	)

	fmt.Printf("\nToken count: %d\n", finalAnswer.PromptEvalCount)

	return finalAnswer.Response, links
}
func lightCodeMode(state *State, question string) (string, []string) {
	state.Logger.Debug("Triggering light code mode")
	client := &http.Client{}
	links := getLinks(state, client, question)
	var toParse strings.Builder
	for _, link := range links {
		result := getRequest(client, link)
		fmt.Fprintf(&toParse, "%s", result)
	}
	finalAnswer := callLightLLM(
		state,
		fmt.Sprintf(`
		[web pages]
		%s
		[history]
		%s
		[question]
		%s	
		[prompt]
		Please get relavant code example from these web pages
		`, toParse.String(), state.Memory.GetMemoryForModel(), question),
		`You get code examples from web pages
		Rules:
		- Always only return code examples
		- Return code examples relavant to the question
		- **NEVER** respond with anything that is not code
		`,
	)

	fmt.Printf("\nToken count: %d\n", finalAnswer.PromptEvalCount)

	return finalAnswer.Response, links
}
func lookupMode(state *State, question string) string {
	state.Logger.Debug("Triggering lookup mode")
	month := time.Now().Month().String()
	year := time.Now().Year()
	client := &http.Client{}
	lines := getQueriesFromLightLLM(
		state,
		buildPrompt(state, question, ""),
		fmt.Sprintf(`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google queries
		- The current date is %s %d if the user asks about something happening now
		- You reply with between 1 and 3 short google queries
		- If the question references a file look at [file]
		- Keep the queries short ( between 3 to 5 words )
		`, month, year)).Queries

	var sb strings.Builder

	for _, line := range lines {
		query := strings.TrimSpace(line)
		if query == "" {
			continue
		}

		result, err := searxSearch(client, state.Settings.SearxNGUrl, query, 1)

		if err != nil {
			fmt.Println("search failed for query:", query, err)
			continue
		}
		fmt.Fprintf(&sb, "Original query: %s\n\nAnswer:%s\n\n", query, result)
	}

	finalAnswer := callHeavyLLM(
		state,
		buildPrompt(state, question, sb.String()),
		fmt.Sprintf(`You answer quickly and accurately using the provided markdown web snippets.
		Rules:
		- Please **always provide a link** to the article that you got your information from.
		- Please **always cite your sources**
		- Respond with information closest to %s %d
		- If the question references a file look at [file]
		- Use the provided original queries and responses as context
		- Please provide the exact answer for the user's question according to the markdown web snippets provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided markdown web snippets please say so explicitly.
		- You always respond in markdown
		`, month, year),
	)
	fmt.Printf("\nToken Count: %d", finalAnswer.PromptEvalCount)
	return finalAnswer.Response
}
