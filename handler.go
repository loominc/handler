package handler

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/graphql-go/graphql"
)

const (
	ContentTypeJSON           = "application/json"
	ContentTypeGraphQL        = "application/graphql"
	ContentTypeFormURLEncoded = "application/x-www-form-urlencoded"
	ContentTypeMultiPart      = "multipart/form-data"
)

type Handler struct {
	Schema       *graphql.Schema
	pretty       bool
	PanicHandler func(ctx context.Context, err error)
}

type File struct {
	Name string
	Data []byte
}

type RequestOptions struct {
	Query         string                 `json:"query" url:"query" schema:"query"`
	Variables     map[string]interface{} `json:"variables" url:"variables" schema:"variables"`
	OperationName string                 `json:"operationName" url:"operationName" schema:"operationName"`
	Files         map[string]File
}

// a workaround for getting`variables` as a JSON string
type requestOptionsCompatibility struct {
	Query         string `json:"query" url:"query" schema:"query"`
	Variables     string `json:"variables" url:"variables" schema:"variables"`
	OperationName string `json:"operationName" url:"operationName" schema:"operationName"`
}

type contextKey int

const (
	FileContextKey contextKey = iota
)

func getFromForm(values url.Values) *RequestOptions {
	query := values.Get("query")
	if query != "" {
		// get variables map
		var variables map[string]interface{}
		variablesStr := values.Get("variables")
		json.Unmarshal([]byte(variablesStr), &variables)

		return &RequestOptions{
			Query:         query,
			Variables:     variables,
			OperationName: values.Get("operationName"),
		}
	}

	return nil
}

// RequestOptions Parses a http.Request into GraphQL request options struct
func NewRequestOptions(r *http.Request) *RequestOptions {
	if reqOpt := getFromForm(r.URL.Query()); reqOpt != nil {
		return reqOpt
	}

	if r.Method != "POST" {
		return &RequestOptions{}
	}

	if r.Body == nil {
		return &RequestOptions{}
	}

	// TODO: improve Content-Type handling
	contentTypeStr := r.Header.Get("Content-Type")
	contentTypeTokens := strings.Split(contentTypeStr, ";")
	contentType := contentTypeTokens[0]

	switch contentType {
	case ContentTypeGraphQL:
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return &RequestOptions{}
		}
		return &RequestOptions{
			Query: string(body),
		}
	case ContentTypeFormURLEncoded:
		if err := r.ParseForm(); err != nil {
			return &RequestOptions{}
		}

		if reqOpt := getFromForm(r.PostForm); reqOpt != nil {
			return reqOpt
		}

		return &RequestOptions{}
	case ContentTypeMultiPart:
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32 MB
			return &RequestOptions{}
		}

		if reqOpt := getFromForm(r.PostForm); reqOpt != nil {
			reqOpt.Files = make(map[string]File)

			for formKey, headers := range r.MultipartForm.File {
				for _, header := range headers {
					file, err := header.Open()
					if err != nil {
						return &RequestOptions{}
					}

					data, err := ioutil.ReadAll(file)
					if err != nil {
						return &RequestOptions{}
					}

					reqOpt.Files[formKey] = File{
						Name: header.Filename,
						Data: data,
					}
				}
			}

			return reqOpt
		}

		return &RequestOptions{}
	case ContentTypeJSON:
		fallthrough
	default:
		var opts RequestOptions
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return &opts
		}
		err = json.Unmarshal(body, &opts)
		if err != nil {
			// Probably `variables` was sent as a string instead of an object.
			// So, we try to be polite and try to parse that as a JSON string
			var optsCompatible requestOptionsCompatibility
			json.Unmarshal(body, &optsCompatible)
			json.Unmarshal([]byte(optsCompatible.Variables), &opts.Variables)
		}
		return &opts
	}
}

// ContextHandler provides an entrypoint into executing graphQL queries with a
// user-provided context.
func (h *Handler) ContextHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// get query
	opts := NewRequestOptions(r)

	// execute graphql query
	params := graphql.Params{
		Schema:         *h.Schema,
		RequestString:  opts.Query,
		VariableValues: opts.Variables,
		OperationName:  opts.OperationName,
		Context:        context.WithValue(ctx, FileContextKey, opts.Files),
		PanicHandler:   h.PanicHandler,
	}
	result := graphql.Do(params)
	if result.HasErrors() {
		log.Printf("GraphQL Error: %v\n", result.Errors)
	}

	if h.pretty {
		w.WriteHeader(http.StatusOK)
		buff, _ := json.MarshalIndent(result, "", "\t")

		w.Write(buff)
	} else {
		w.WriteHeader(http.StatusOK)
		buff, _ := json.Marshal(result)

		w.Write(buff)
	}
}

// ServeHTTP provides an entrypoint into executing graphQL queries.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ContextHandler(r.Context(), w, r)
}

type Config struct {
	Schema       *graphql.Schema
	Pretty       bool
	PanicHandler func(ctx context.Context, err error)
}

func NewConfig() *Config {
	return &Config{
		Schema: nil,
		Pretty: true,
	}
}

func New(p *Config) *Handler {
	if p == nil {
		p = NewConfig()
	}
	if p.Schema == nil {
		panic("undefined GraphQL schema")
	}

	return &Handler{
		Schema:       p.Schema,
		pretty:       p.Pretty,
		PanicHandler: p.PanicHandler,
	}
}
