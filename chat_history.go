package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

type ChatInteraction struct {
	Question string
	Answer   string
	Links    []string
}

func (self ChatInteraction) GetTags() string {
	return fmt.Sprintf(`
		User: %s
		Model: %s
	`, self.Question, self.Answer)
}

type Memory struct {
	Title        string
	Id           string
	Interactions []ChatInteraction
}

func (self Memory) GetMemoryForModel() string {
	var history strings.Builder
	for _, interaction := range self.Interactions {
		fmt.Fprintf(&history, "%s\n", interaction.GetTags())
	}

	return history.String()
}
func (self Memory) GetPrintedMemory(renderer *glamour.TermRenderer) string{

	var history strings.Builder
	for _, interaction := range self.Interactions {
		fmt.Fprintf(&history, ">>>> %s\n\n", interaction.Question)
		fmt.Fprintf(&history, "LLM: %s", interaction.Answer)
		fmt.Fprintf(&history, "Links: \n%s", strings.Join(interaction.Links, "\n"))
	}

	return history.String()

}
