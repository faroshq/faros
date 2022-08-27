package interactive

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	"github.com/faroshq/faros/pkg/models"
)

func InterfactiveAskNamespace(namespaces []models.Namespace) (string, error) {
	answers := struct {
		Namespace string `survey:"Namepace"`
	}{}

	// construct project question
	namespaceSelect := &survey.Select{
		Message: "Choose a Namespace:",
		Options: []string{""},
	}
	for _, namespace := range namespaces {
		namespaceSelect.Options = append(namespaceSelect.Options, fmt.Sprintf("%s :%s ", namespace.Name, namespace.ID))
	}
	namespaceSelect.Default = fmt.Sprintf("%s :%s ", namespaces[0].Name, namespaces[0].ID)

	var projectQ = []*survey.Question{
		{
			Name:   "Namespace",
			Prompt: namespaceSelect,
		},
	}

	// perform the questions
	err := survey.Ask(projectQ, &answers)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(strings.Split(answers.Namespace, ":")[1]), nil
}
