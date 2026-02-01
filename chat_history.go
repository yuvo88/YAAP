package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

type ChatInteraction struct {
	Question string
	Answer   string
}

func (self ChatInteraction) GetTags() string {
	return fmt.Sprintf(`
		User: %s
		Model: %s
	`, self.Question, self.Answer)
}

type History struct {
	Title string
	Interactions []ChatInteraction
}

func (self History) GetHistoryForModel() string {
	var history strings.Builder
	for _, interaction := range self.Interactions {
		fmt.Fprintf(&history, "%s\n", interaction.GetTags())
	}

	return history.String()
}
func (self History) PrintHistory(renderer *glamour.TermRenderer) {
	for _, interaction := range self.Interactions {
		fmt.Printf("> %s\n", interaction.Question)
		markdown, _ := renderer.Render(interaction.Answer)
		fmt.Println(markdown)
	}

}



