package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

// apiCommand sends one GraphQL document to webapp-api as the signed-in developer and prints the server's response,
// data and errors together, as JSON. It has no operations of its own: the server allows or refuses whatever the
// document asks for, for the signed-in user. It exits 0 for a successful GraphQL response, 3 when the server says to
// upgrade, and 1 for anything else (see answerExit).
func apiCommand(command commandFunc) *cobra.Command {
	var query, queryFile, variables, scope string
	cmd := command("api", "Send a GraphQL document to the MultiPaaS API as you and print the response", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		request, err := apiRequest(s.env.stdin, query, queryFile, variables)
		if err != nil {
			return nil, nil, err
		}
		response, err := s.send(ctx, scope, request)
		if err != nil {
			return nil, nil, err
		}
		if err := writeJSON(s.env.stdout, response.body); err != nil {
			return nil, nil, err
		}
		return nil, nil, &printedResponse{exit: answerExit(response)}
	})
	cmd.Flags().StringVar(&query, "query", "", "the GraphQL document to send")
	cmd.Flags().StringVar(&queryFile, "query-file", "", "read the document from this file, or from stdin for -")
	cmd.Flags().StringVar(&variables, "variables", "", "the document's variables, as a JSON object")
	cmd.Flags().StringVar(&scope, "scope", "", "send the request in this scope: <client id>[/<app id>[/<app-tenant id>]] (default none)")
	cmd.MarkFlagsMutuallyExclusive("query", "query-file")
	cmd.MarkFlagsOneRequired("query", "query-file")
	return cmd
}

// printedResponse ends the api command once it has printed the server's response: report prints nothing more and
// exits with exit.
type printedResponse struct{ exit int }

func (p *printedResponse) Error() string { return fmt.Sprintf("response printed, exit %d", p.exit) }

// apiRequest is the GraphQL request body: the document from --query or --query-file, and --variables when given,
// which must be a JSON object.
func apiRequest(stdin io.Reader, query, queryFile, variables string) ([]byte, error) {
	document, err := textOrFile(stdin, query, queryFile, "--query-file")
	if err != nil {
		return nil, err
	}
	body := map[string]any{"query": document}
	if variables != "" {
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(variables), &object); err != nil || object == nil {
			return nil, fmt.Errorf("--variables must be a JSON object")
		}
		body["variables"] = json.RawMessage(variables)
	}
	return json.Marshal(body)
}

// send posts one GraphQL request to webapp-api as the developer and returns the response body. Like call, it
// refreshes once and retries when the server refused the token. Unlike call, it retries only a refusal of the whole
// request, never one a field raised, so a mutation never runs twice.
func (s *session) send(ctx context.Context, scope string, request []byte) (apiResponse, error) {
	token, err := s.token(ctx, "")
	if err != nil {
		return apiResponse{}, err
	}
	response, err := s.post(ctx, token, scope, request)
	if err != nil || !refusedToken(response.body) {
		return response, err
	}
	if token, err = s.token(ctx, token); err != nil {
		return apiResponse{}, err
	}
	return s.post(ctx, token, scope, request)
}

// apiResponse is what webapp-api answered: the JSON body and the HTTP status it came with.
type apiResponse struct {
	body   json.RawMessage
	status int
}

// post sends request with the headers every webapp-api request carries. Whatever the HTTP status, a JSON body is
// returned with it for the caller to print; any other body is a CLI_ERROR.
func (s *session) post(ctx context.Context, token, scope string, request []byte) (apiResponse, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, s.origins.webappAPI+"/graphql", bytes.NewReader(request))
	if err != nil {
		return apiResponse{}, err
	}
	httpRequest.Header = s.headers(token, scope)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := s.http.Do(httpRequest)
	if err != nil {
		return apiResponse{}, err
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return apiResponse{}, err
	}
	if !json.Valid(body) {
		return apiResponse{}, fmt.Errorf("webapp-api answered HTTP %d with a body that is not JSON", httpResponse.StatusCode)
	}
	return apiResponse{body: body, status: httpResponse.StatusCode}, nil
}

// refusedToken is whether the server refused the request's token before running any of it: an UNAUTHORIZED error
// no field raised (it has no path), and no data.
func refusedToken(response json.RawMessage) bool {
	var answer graphQLAnswer
	if json.Unmarshal(response, &answer) != nil || len(answer.Errors) == 0 {
		return false
	}
	first := answer.Errors[0]
	return first.Extensions.Code == "UNAUTHORIZED" && len(first.Path) == 0 && (len(answer.Data) == 0 || string(answer.Data) == "null")
}

// answerExit is the api command's exit code for a response: 3 when its first error says to upgrade, 1 for any other
// error, and 1 for anything that is not a successful GraphQL response (a non-2xx status, or a body whose data is not
// an object). It is 0 only for a 2xx response with a data object and no errors.
func answerExit(response apiResponse) int {
	var answer graphQLAnswer
	var keys map[string]json.RawMessage
	if json.Unmarshal(response.body, &answer) != nil || json.Unmarshal(response.body, &keys) != nil {
		return exitRefused
	}
	switch {
	case len(answer.Errors) > 0 && answer.Errors[0].Extensions.Code == "UNSUPPORTED_CLI_VERSION":
		return exitUpgrade
	case len(answer.Errors) > 0, response.status < 200, response.status > 299:
		return exitRefused
	}
	if !bytes.HasPrefix(bytes.TrimSpace(keys["data"]), []byte("{")) {
		return exitRefused
	}
	return exitOK
}

// graphQLAnswer is the part of a webapp-api response the api command reads to choose whether to retry and its exit code.
type graphQLAnswer struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Path       []any `json:"path"`
		Extensions struct {
			Code string `json:"code"`
		} `json:"extensions"`
	} `json:"errors"`
}
