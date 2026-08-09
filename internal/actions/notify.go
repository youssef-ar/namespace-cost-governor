package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type SlackMessage struct {
	Text        string            `json:"text"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

type SlackAttachment struct {
	Color  string       `json:"color,omitempty"`
	Fields []SlackField `json:"fields,omitempty"`
}

type SlackField struct {
	Title string `json:"title,omitempty"`
	Value string `json:"value,omitempty"`
	Short bool   `json:"short,omitempty"`
}

func SendSlack(ctx context.Context, webhookURL string, msg SlackMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling slack message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending slack message: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned non-200: %d", resp.StatusCode)
	}

	return nil
}

func BuildMessage(namespace, phase string, budgetPercent int, affected []string) SlackMessage {
	color := "warning"
	if phase == "Exceeded" || phase == "Suspended" {
		color = "danger"
	}

	affectedStr := "none"
	if len(affected) > 0 {
		affectedStr = strings.Join(affected, ", ")
	}

	return SlackMessage{
		Text: fmt.Sprintf("*[%s]* namespace `%s` budget alert", phase, namespace),
		Attachments: []SlackAttachment{
			{
				Color: color,
				Fields: []SlackField{
					{Title: "Namespace", Value: namespace, Short: true},
					{Title: "Phase", Value: phase, Short: true},
					{Title: "Budget used", Value: fmt.Sprintf("%d%%", budgetPercent), Short: true},
					{Title: "Workloads affected", Value: affectedStr, Short: false},
				},
			},
		},
	}
}
