package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// feedbackCommands are a builder's reports about MultiPaaS itself: send one, list and show their own,
// and read or set whether Claude may send without asking.
func feedbackCommands(command commandFunc) *cobra.Command {
	return group("feedback", "Report a MultiPaaS bug or feature request, and follow your reports",
		feedbackSend(command), feedbackList(command), feedbackShow(command), feedbackSettings(command))
}

// feedbackSend files a report. It is about --app's application, else multipaas.json's, else about no
// application; --general sends no scope, so it files even when that application is no longer reachable.
func feedbackSend(command commandFunc) *cobra.Command {
	var kind, summary, details, detailsFile, appID, via string
	var general bool
	cmd := command("send", "Send a bug report or feature request to MultiPaaS", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		text, err := feedbackDetails(s.env.stdin, details, detailsFile)
		if err != nil {
			return nil, nil, err
		}
		input, scopeApp, err := feedbackInput(kind, summary, text, appID, via, general)
		if err != nil {
			return nil, nil, err
		}
		scope := ""
		if scopeApp != "" {
			if scope, err = s.appScope(ctx, scopeApp, ""); err != nil {
				return nil, nil, err
			}
		}
		var sent FeedbackParts
		err = s.call(ctx, scope, func(client graphql.Client) error {
			response, err := SubmitFeedback(ctx, client, input)
			if err == nil {
				sent = response.SubmitFeedback.FeedbackParts
			}
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		return map[string]FeedbackParts{"feedback": sent}, func(out io.Writer) {
			fmt.Fprintf(out, "Sent %s %s (%s)\n", sent.Kind, sent.Id, sent.Status)
		}, nil
	})
	cmd.Flags().StringVar(&kind, "kind", "feature", "bug (something doesn't work as expected) or feature (something you'd like to see)")
	cmd.Flags().StringVar(&summary, "summary", "", "a one-line title (without one, list and show use the first line of the details)")
	cmd.Flags().StringVar(&details, "details", "", "what happened and what you expected")
	cmd.Flags().StringVar(&detailsFile, "details-file", "", "read the details from this file, or from stdin for -")
	cmd.Flags().StringVar(&appID, "app", "", "the application it's about (default: multipaas.json's applicationId)")
	cmd.Flags().BoolVar(&general, "general", false, "about MultiPaaS in general, not an application")
	cmd.Flags().StringVar(&via, "via", "", "plugin, when Claude sends it for you")
	cmd.MarkFlagsMutuallyExclusive("details", "details-file")
	cmd.MarkFlagsOneRequired("details", "details-file")
	cmd.MarkFlagsMutuallyExclusive("app", "general")
	return cmd
}

// feedbackList is the developer's own reports across every client, newest first, so it sends no scope.
func feedbackList(command commandFunc) *cobra.Command {
	return command("list", "List your feedback reports and their status", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list, err := pages(func(page PageInput) ([]FeedbackParts, bool, error) {
			var listed *MyFeedbackResponse
			err := s.call(ctx, "", func(client graphql.Client) (err error) {
				listed, err = MyFeedback(ctx, client, page)
				return err
			})
			if err != nil {
				return nil, false, err
			}
			reports := []FeedbackParts{}
			for _, item := range listed.MyFeedback.Items {
				reports = append(reports, item.FeedbackParts)
			}
			return reports, listed.MyFeedback.PageInfo.HasMore, nil
		})
		return list, func(out io.Writer) {
			table(out, "ID\tKIND\tSTATUS\tUPDATED\tSUMMARY", list, func(f FeedbackParts) string {
				return fmt.Sprintf("%s\t%s\t%s\t%s\t%s", f.Id, f.Kind, f.Status, f.UpdatedAt, feedbackTitle(f.Summary, f.Details))
			})
		}, err
	})
}

// feedbackShow is one of the developer's own reports with its status history, sent with no scope.
func feedbackShow(command commandFunc) *cobra.Command {
	var id string
	cmd := command("show", "Show one of your feedback reports and its status history", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var report *MyFeedbackReportMyFeedbackReportFeedback
		err := s.call(ctx, "", func(client graphql.Client) error {
			response, err := MyFeedbackReport(ctx, client, id)
			if err == nil {
				report = response.MyFeedbackReport
			}
			return err
		})
		if err == nil && report == nil {
			err = &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no feedback report " + id + " of yours"}
		}
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"feedback": report}, func(out io.Writer) {
			fmt.Fprintln(out, feedbackTitle(report.Summary, report.Details))
			fmt.Fprintf(out, "%s, %s, %s\n", report.Kind, report.Status, feedbackApplication(report.FeedbackParts))
			if report.Client != nil {
				fmt.Fprintf(out, "client %s\n", report.Client.Name)
			}
			fmt.Fprintln(out)
			table(out, "STATUS\tCHANGED\tNOTE", report.History, func(change MyFeedbackReportMyFeedbackReportFeedbackHistoryFeedbackStatusChange) string {
				note := ""
				if change.Note != nil {
					note = *change.Note
				}
				return fmt.Sprintf("%s\t%s\t%s", change.Status, change.ChangedAt, note)
			})
		}, nil
	})
	cmd.Flags().StringVar(&id, "id", "", "the report's id (from feedback list)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

// feedbackSettings reads the developer's own setting, or sets it when --auto-send is given. Both send no scope.
func feedbackSettings(command commandFunc) *cobra.Command {
	var autoSend string
	cmd := command("settings", "Show or set whether Claude sends your feedback without asking", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		if autoSend != "" && autoSend != "on" && autoSend != "off" {
			return nil, nil, &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: "--auto-send must be on or off"}
		}
		var current bool
		err := s.call(ctx, "", func(client graphql.Client) error {
			if autoSend == "" {
				response, err := FeedbackSetting(ctx, client)
				if err == nil {
					current = response.FeedbackSetting.AutoSend
				}
				return err
			}
			response, err := SetFeedbackSetting(ctx, client, autoSend == "on")
			if err == nil {
				current = response.SetFeedbackSetting.AutoSend
			}
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		return map[string]bool{"autoSend": current}, func(out io.Writer) {
			if current {
				fmt.Fprintln(out, "Auto-send is on")
			} else {
				fmt.Fprintln(out, "Auto-send is off")
			}
		}, nil
	})
	cmd.Flags().StringVar(&autoSend, "auto-send", "", "on or off: let Claude send your feedback without asking first")
	return cmd
}

// feedbackInput builds submitFeedback's input from the flags given, and names the application to scope the
// request to ("" for none, always for --general). apiVersion comes from multipaas.json only when the report is
// about its application.
func feedbackInput(kind, summary, details, appID, via string, general bool) (map[string]any, string, error) {
	if kind != "bug" && kind != "feature" {
		return nil, "", &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: "--kind must be bug or feature"}
	}
	if via != "" && via != "plugin" {
		return nil, "", &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: "--via must be plugin"}
	}
	project, err := readProject()
	if err != nil {
		return nil, "", err
	}
	input := map[string]any{"kind": kind, "details": details}
	if summary != "" {
		input["summary"] = summary
	}
	if via == "plugin" {
		input["viaPlugin"] = true
	}
	if general {
		input["aboutApplication"] = false
		return input, "", nil
	}
	scopeApp := appID
	if scopeApp == "" {
		scopeApp = project.ApplicationID
	}
	if scopeApp != "" && scopeApp == project.ApplicationID && project.APIVersion != "" {
		input["apiVersion"] = project.APIVersion
	}
	return input, scopeApp, nil
}

// feedbackDetails is --details, or the contents of --details-file, where - is stdin.
func feedbackDetails(stdin io.Reader, details, detailsFile string) (string, error) {
	if detailsFile == "" {
		return details, nil
	}
	var data []byte
	var err error
	if detailsFile == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(detailsFile)
	}
	if err != nil {
		return "", fmt.Errorf("read --details-file: %w", err)
	}
	return string(data), nil
}

// feedbackApplication names the application a report is about. A report sent from a scope that has since been
// removed, whether its application or its client, says so.
func feedbackApplication(report FeedbackParts) string {
	switch {
	case report.Application != nil:
		return report.Application.Name
	case report.SentFromRemovedScope:
		return "sent from a scope that has since been removed"
	}
	return "no application"
}

// feedbackTitle is a report's summary, else the first line of its details.
func feedbackTitle(summary *string, details string) string {
	if summary != nil {
		return *summary
	}
	title, _, _ := strings.Cut(details, "\n")
	return title
}
