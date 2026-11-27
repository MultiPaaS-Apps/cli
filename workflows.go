package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// workflowCommands declare, change, list and delete the application's workflows, and read their runs. Create,
// update, delete and runs need model.edit; list and show do not.
func workflowCommands(command commandFunc) *cobra.Command {
	return group("workflows", "Declare, change, list and delete the application's workflows, and read their runs",
		workflowList(command), workflowShow(command), workflowCreate(command), workflowUpdate(command), workflowDelete(command), workflowRuns(command))
}

func workflowList(command commandFunc) *cobra.Command {
	var appID string
	cmd := command("list", "List the application's workflows", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		listed := []WorkflowParts{}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			var err error
			listed, err = workflows(ctx, client)
			return err
		})
		return map[string]any{"workflows": listed}, func(out io.Writer) {
			table(out, "KEY\tNAME\tTRIGGER\tSTEPS\tID", listed, func(w WorkflowParts) string {
				return w.Key + "\t" + w.Name + "\t" + triggerOf(w).Kind + "\t" + fmt.Sprint(len(w.Steps)) + "\t" + w.Id
			})
		}, err
	})
	appFlag(cmd, &appID)
	return cmd
}

func workflowShow(command commandFunc) *cobra.Command {
	var appID, key string
	cmd := command("show <key>", "Show a workflow: its trigger, who it runs as and its steps", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var found WorkflowParts
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			var err error
			found, err = workflowByKey(ctx, client, key)
			return err
		})
		return map[string]any{"workflow": found}, func(out io.Writer) { printWorkflow(out, found) }, err
	})
	cmd.Args = oneID(&key)
	appFlag(cmd, &appID)
	return cmd
}

func workflowCreate(command commandFunc) *cobra.Command {
	var appID, file string
	cmd := command("create", "Declare a workflow from a definition file", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		definition, err := readDefinition(s, file)
		if err != nil {
			return nil, nil, err
		}
		var created WorkflowParts
		err = s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := CreateWorkflow(ctx, client, definition)
			if err == nil {
				created = response.CreateWorkflow.WorkflowParts
			}
			return err
		})
		return map[string]any{"workflow": created}, func(out io.Writer) { fmt.Fprintf(out, "Created workflow %s (%s)\n", created.Key, created.Id) }, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&file, "file", "", "the definition file, or - for stdin")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func workflowUpdate(command commandFunc) *cobra.Command {
	var appID, key, file string
	cmd := command("update <key>", "Replace a workflow's definition from a file", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		definition, err := readDefinition(s, file)
		if err != nil {
			return nil, nil, err
		}
		var updated WorkflowParts
		err = s.inApp(ctx, appID, func(client graphql.Client) error {
			found, err := workflowByKey(ctx, client, key)
			if err != nil {
				return err
			}
			response, err := UpdateWorkflow(ctx, client, found.Id, definition)
			if err == nil {
				updated = response.UpdateWorkflow.WorkflowParts
			}
			return err
		})
		return map[string]any{"workflow": updated}, func(out io.Writer) { fmt.Fprintf(out, "Updated workflow %s (%s)\n", updated.Key, updated.Id) }, err
	})
	cmd.Args = oneID(&key)
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&file, "file", "", "the definition file, or - for stdin")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func workflowDelete(command commandFunc) *cobra.Command {
	var appID, key string
	cmd := command("delete <key>", "Delete a workflow", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var deleted WorkflowParts
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			found, err := workflowByKey(ctx, client, key)
			if err != nil {
				return err
			}
			response, err := DeleteWorkflow(ctx, client, found.Id)
			if err == nil {
				deleted = response.DeleteWorkflow.WorkflowParts
			}
			return err
		})
		return map[string]any{"workflow": deleted}, func(out io.Writer) { fmt.Fprintf(out, "Deleted workflow %s (%s)\n", deleted.Key, deleted.Id) }, err
	})
	cmd.Args = oneID(&key)
	appFlag(cmd, &appID)
	return cmd
}

// workflowRun is one run as workflows runs prints it.
type workflowRun struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	TriggerKind    string          `json:"triggerKind"`
	Trigger        json.RawMessage `json:"trigger"`
	RunAs          *runUser        `json:"runAs"`
	RecordsChanged int             `json:"recordsChanged"`
	CreatedAt      string          `json:"createdAt"`
	FinishedAt     *string         `json:"finishedAt"`
	FailedStep     string          `json:"failedStep"`
	Error          string          `json:"error"`
}

// runUser is who a run ran as; a run's runAs is null once that user can no longer be resolved.
type runUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// ranAs is the column workflows runs prints for who ran it.
func (r workflowRun) ranAs() string {
	switch {
	case r.RunAs == nil:
		return "unknown user"
	case r.RunAs.Email != "":
		return r.RunAs.Email
	case r.RunAs.Name != "":
		return r.RunAs.Name
	}
	return r.RunAs.ID
}

// runOf reads a listed run through its JSON, the shape the query selects, so it needs no name for genqlient's nested types.
func runOf(item any) workflowRun {
	var read struct {
		ID, Status, TriggerKind, CreatedAt string
		Trigger                            json.RawMessage
		RecordsChanged                     int
		FinishedAt                         *string
		RunAs                              *runUser
		Attempts                           []struct {
			FailedStepKey string
			Error         json.RawMessage
		}
	}
	data, _ := json.Marshal(item)
	_ = json.Unmarshal(data, &read)
	run := workflowRun{ID: read.ID, Status: read.Status, TriggerKind: read.TriggerKind, Trigger: read.Trigger,
		RunAs: read.RunAs, RecordsChanged: read.RecordsChanged, CreatedAt: read.CreatedAt, FinishedAt: read.FinishedAt}
	if last := len(read.Attempts) - 1; last >= 0 {
		run.FailedStep = read.Attempts[last].FailedStepKey
		var failure struct{ Message string }
		_ = json.Unmarshal(read.Attempts[last].Error, &failure)
		run.Error = failure.Message
	}
	return run
}

func workflowRuns(command commandFunc) *cobra.Command {
	var appID, envID, key string
	cmd := command("runs <key>", "List a workflow's runs in the environment, newest first", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		runs := []workflowRun{}
		err := s.inEnv(ctx, appID, envID, func(client graphql.Client) error {
			found, err := workflowByKey(ctx, client, key)
			if err != nil {
				return err
			}
			runs, err = pages(func(page PageInput) ([]workflowRun, bool, error) {
				response, err := WorkflowRuns(ctx, client, found.Id, page)
				if err != nil {
					return nil, false, err
				}
				listed := []workflowRun{}
				for _, item := range response.WorkflowRuns.Items {
					listed = append(listed, runOf(item))
				}
				return listed, response.WorkflowRuns.PageInfo.HasMore, nil
			})
			return err
		})
		return map[string]any{"runs": runs}, func(out io.Writer) {
			table(out, "WHEN\tSTATUS\tRAN AS\tRECORDS\tFAILED STEP\tERROR", runs, func(r workflowRun) string {
				return fmt.Sprintf("%s\t%s\t%s\t%d\t%s\t%s", r.CreatedAt, r.Status, r.ranAs(), r.RecordsChanged, r.FailedStep, r.Error)
			})
		}, err
	})
	cmd.Args = oneID(&key)
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	return cmd
}

func workflows(ctx context.Context, client graphql.Client) ([]WorkflowParts, error) {
	response, err := Workflows(ctx, client)
	if err != nil {
		return nil, err
	}
	listed := make([]WorkflowParts, 0, len(response.Workflows))
	for _, item := range response.Workflows {
		listed = append(listed, item.WorkflowParts)
	}
	return listed, nil
}

// workflowByKey finds the workflow whose key is key among the application's.
func workflowByKey(ctx context.Context, client graphql.Client, key string) (WorkflowParts, error) {
	listed, err := workflows(ctx, client)
	if err != nil {
		return WorkflowParts{}, err
	}
	i := slices.IndexFunc(listed, func(w WorkflowParts) bool { return w.Key == key })
	if i < 0 {
		return WorkflowParts{}, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no workflow " + key}
	}
	return listed[i], nil
}

// readDefinition is the JSON in the file --file names, or on stdin for -. It is sent as the definition unchanged.
func readDefinition(s *session, file string) (json.RawMessage, error) {
	text, err := textOrFile(s.env.stdin, "", file, "--file")
	if err != nil {
		return nil, err
	}
	var definition json.RawMessage
	if err := json.Unmarshal([]byte(text), &definition); err != nil {
		return nil, fmt.Errorf("--file is not valid JSON: %w", err)
	}
	return definition, nil
}

// trigger is the part of a workflow's trigger the CLI prints; the server owns the rest of its shape.
type trigger struct {
	Kind   string         `json:"kind"`
	Inputs []triggerInput `json:"inputs"`
}

type triggerInput struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

func triggerOf(w WorkflowParts) trigger {
	var t trigger
	_ = json.Unmarshal(w.Trigger, &t)
	return t
}

func printWorkflow(out io.Writer, w WorkflowParts) {
	fmt.Fprintf(out, "%s (%s)  %s\n", w.Name, w.Key, w.Id)
	if w.Description != "" {
		fmt.Fprintln(out, w.Description)
	}
	t := triggerOf(w)
	fmt.Fprintf(out, "Trigger: %s", t.Kind)
	if len(t.Inputs) > 0 {
		inputs := make([]string, 0, len(t.Inputs))
		for _, input := range t.Inputs {
			need := "optional"
			if input.Required {
				need = "required"
			}
			inputs = append(inputs, fmt.Sprintf("%s (%s, %s)", input.Key, input.Type, need))
		}
		fmt.Fprintf(out, " with inputs %s", strings.Join(inputs, ", "))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Runs as the caller")
	for i, step := range w.Steps {
		fmt.Fprintf(out, "%d. %s — %s\n", i+1, step.Name, step.Description)
	}
}
