package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// urlNameCommands show and rename the URL names a site's hostname is built from: an app's, an environment's and the
// app's client's.
func urlNameCommands(command commandFunc) *cobra.Command {
	return group("url-name", "The URL names your sites' hostnames are built from", urlNameShow(command), urlNameRename(command))
}

// urlNameTarget is the URL name a command acts on: the client's with --client, an environment's with --env, else the app's.
type urlNameTarget struct {
	appID, envID string
	client       bool
}

func (t *urlNameTarget) flags(cmd *cobra.Command) {
	appFlag(cmd, &t.appID)
	cmd.Flags().BoolVar(&t.client, "client", false, "the URL name of the app's client")
	cmd.Flags().StringVar(&t.envID, "env", "", "the environment's id (from envs list)")
	cmd.MarkFlagsMutuallyExclusive("client", "env")
}

// urlNameView is one owner's URL name as the commands print it. ChangeableAt is set only for a client inside its
// monthly limit, and SiteURL only for an environment.
type urlNameView struct {
	Kind         string              `json:"kind"`
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	ChangeableAt *string             `json:"changeableAt,omitempty"`
	SiteURL      string              `json:"siteUrl,omitempty"`
	History      []UrlNameEntryParts `json:"history"`
}

func urlNameShow(command commandFunc) *cobra.Command {
	var target urlNameTarget
	cmd := command("show", "Show a URL name and the names it had before", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var view urlNameView
		err := s.inApp(ctx, target.appID, func(client graphql.Client) error {
			var err error
			view, err = target.read(ctx, client)
			return err
		})
		return map[string]urlNameView{"urlName": view}, view.print, err
	})
	target.flags(cmd)
	return cmd
}

func urlNameRename(command commandFunc) *cobra.Command {
	var target urlNameTarget
	var name string
	var confirmTakeover bool
	cmd := command("rename", "Change a URL name", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var view urlNameView
		err := s.inApp(ctx, target.appID, func(client graphql.Client) error {
			var err error
			view, err = target.rename(ctx, client, name, confirmTakeover)
			return err
		})
		return map[string]urlNameView{"urlName": view}, view.print, err
	})
	cmd.Long = "Changes the URL name of the app, of an environment with --env, or of the app's client with --client. " +
		"A client's can change once a month, and its old name stays held for it. Taking a name another app or " +
		"environment used before needs --confirm-takeover: that one's old address stops pointing to it.\n\n" +
		"Renaming the app needs environment.manage in every environment, an environment needs environment.manage there, " +
		"and the client needs client.access.manage in the client, or the operator permission platform.clients.manage."
	target.flags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "the new URL name")
	_ = cmd.MarkFlagRequired("name")
	cmd.Flags().BoolVar(&confirmTakeover, "confirm-takeover", false, "take a name another app or environment used before")
	cmd.MarkFlagsMutuallyExclusive("client", "confirm-takeover")
	return cmd
}

func (t urlNameTarget) read(ctx context.Context, client graphql.Client) (urlNameView, error) {
	switch {
	case t.client:
		response, err := ClientUrlName(ctx, client)
		if err != nil {
			return urlNameView{}, err
		}
		c := response.Me.Client
		return urlNameView{Kind: "client", ID: c.Id, Name: c.UrlName, ChangeableAt: c.UrlNameChangeableAt, History: historyOf(c.UrlNameHistory)}, nil
	case t.envID != "":
		response, err := EnvironmentUrlNames(ctx, client)
		if err != nil {
			return urlNameView{}, err
		}
		for _, e := range response.Me.Application.Environments {
			if strings.EqualFold(e.Id, t.envID) {
				return urlNameView{Kind: "environment", ID: e.Id, Name: e.UrlName, SiteURL: e.SiteUrl, History: historyOf(e.UrlNameHistory)}, nil
			}
		}
		return urlNameView{}, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "environment not found"}
	default:
		response, err := ApplicationUrlName(ctx, client)
		if err != nil {
			return urlNameView{}, err
		}
		a := response.Me.Application
		return urlNameView{Kind: "application", ID: a.Id, Name: a.UrlName, History: historyOf(a.UrlNameHistory)}, nil
	}
}

func (t urlNameTarget) rename(ctx context.Context, client graphql.Client, name string, confirmTakeover bool) (urlNameView, error) {
	switch {
	case t.client:
		current, err := ClientUrlName(ctx, client)
		if err != nil {
			return urlNameView{}, err
		}
		response, err := RenameClientUrlName(ctx, client, current.Me.Client.Id, name)
		if err != nil {
			return urlNameView{}, err
		}
		c := response.RenameClientUrlName
		return urlNameView{Kind: "client", ID: c.Id, Name: c.UrlName, ChangeableAt: c.UrlNameChangeableAt, History: historyOf(c.UrlNameHistory)}, nil
	case t.envID != "":
		response, err := RenameEnvironmentUrlName(ctx, client, t.envID, name, confirmTakeover)
		if err != nil {
			return urlNameView{}, err
		}
		e := response.RenameEnvironmentUrlName
		return urlNameView{Kind: "environment", ID: e.Id, Name: e.UrlName, SiteURL: e.SiteUrl, History: historyOf(e.UrlNameHistory)}, nil
	default:
		response, err := RenameApplicationUrlName(ctx, client, t.appID, name, confirmTakeover)
		if err != nil {
			return urlNameView{}, err
		}
		a := response.RenameApplicationUrlName
		return urlNameView{Kind: "application", ID: a.Id, Name: a.UrlName, History: historyOf(a.UrlNameHistory)}, nil
	}
}

func (v urlNameView) print(out io.Writer) {
	fmt.Fprintf(out, "%s %s url name %q\n", v.Kind, v.ID, v.Name)
	if v.SiteURL != "" {
		fmt.Fprintf(out, "site %s\n", v.SiteURL)
	}
	if v.ChangeableAt != nil {
		fmt.Fprintf(out, "can change again at %s\n", *v.ChangeableAt)
	}
	table(out, "NAME\tCLAIMED\tREPLACED", v.History, func(e UrlNameEntryParts) string {
		replaced := ""
		if e.ReplacedAt != nil {
			replaced = *e.ReplacedAt
		}
		return e.Name + "\t" + e.ClaimedAt + "\t" + replaced
	})
}

// urlNameEntryGetter is what every generated history entry has: genqlient gives each one getters for its fragment's fields.
type urlNameEntryGetter interface {
	GetName() string
	GetClaimedAt() string
	GetReplacedAt() *string
}

// historyOf turns a generated history list into its fragment's plain entries.
func historyOf[T any, P interface {
	*T
	urlNameEntryGetter
}](items []T) []UrlNameEntryParts {
	history := []UrlNameEntryParts{}
	for i := range items {
		entry := P(&items[i])
		history = append(history, UrlNameEntryParts{Name: entry.GetName(), ClaimedAt: entry.GetClaimedAt(), ReplacedAt: entry.GetReplacedAt()})
	}
	return history
}
