// Command printschema prints a GraphQL introspection response as SDL, keeping every default value, every
// deprecation and every description it carries. It leaves out the introspection types, the built-in scalars
// Boolean, Float, ID, Int and String, and the @deprecated, @include and @skip directives.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/parser"
)

var (
	builtinScalars    = []string{"Boolean", "Float", "ID", "Int", "String"}
	builtinDirectives = []string{"deprecated", "include", "skip"}
)

func main() {
	if err := printSchema(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "printschema:", err)
		os.Exit(1)
	}
}

func printSchema(r io.Reader, w io.Writer) error {
	var response struct {
		Data struct {
			Schema introspectionSchema `json:"__schema"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&response); err != nil {
		return fmt.Errorf("decode the introspection response: %w", err)
	}
	doc, err := schemaDocument(response.Data.Schema)
	if err != nil {
		return err
	}
	formatter.NewFormatter(w).FormatSchemaDocument(doc)
	return nil
}

func schemaDocument(schema introspectionSchema) (*ast.SchemaDocument, error) {
	doc := &ast.SchemaDocument{}
	for _, directive := range schema.Directives {
		if slices.Contains(builtinDirectives, directive.Name) {
			continue
		}
		args, err := argumentDefinitions(directive.Args)
		if err != nil {
			return nil, fmt.Errorf("directive @%s: %w", directive.Name, err)
		}
		locations := make([]ast.DirectiveLocation, len(directive.Locations))
		for i, location := range directive.Locations {
			locations[i] = ast.DirectiveLocation(location)
		}
		doc.Directives = append(doc.Directives, &ast.DirectiveDefinition{
			Description: directive.Description,
			Name:        directive.Name,
			Arguments:   args,
			Locations:   locations,
		})
	}
	for _, typ := range schema.Types {
		if strings.HasPrefix(typ.Name, "__") || (typ.Kind == string(ast.Scalar) && slices.Contains(builtinScalars, typ.Name)) {
			continue
		}
		definition, err := typeDefinition(typ)
		if err != nil {
			return nil, fmt.Errorf("type %s: %w", typ.Name, err)
		}
		doc.Definitions = append(doc.Definitions, definition)
	}
	return doc, nil
}

func typeDefinition(typ introspectionType) (*ast.Definition, error) {
	definition := &ast.Definition{Kind: ast.DefinitionKind(typ.Kind), Name: typ.Name, Description: typ.Description}
	for _, iface := range typ.Interfaces {
		definition.Interfaces = append(definition.Interfaces, iface.Name)
	}
	if typ.Kind == string(ast.Union) {
		for _, member := range typ.PossibleTypes {
			definition.Types = append(definition.Types, member.Name)
		}
	}
	for _, field := range typ.Fields {
		args, err := argumentDefinitions(field.Args)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		definition.Fields = append(definition.Fields, &ast.FieldDefinition{
			Description: field.Description,
			Name:        field.Name,
			Arguments:   args,
			Type:        astType(field.Type),
			Directives:  deprecation(field.IsDeprecated, field.DeprecationReason),
		})
	}
	for _, input := range typ.InputFields {
		defaultValue, err := literal(input.DefaultValue)
		if err != nil {
			return nil, fmt.Errorf("input field %s: %w", input.Name, err)
		}
		definition.Fields = append(definition.Fields, &ast.FieldDefinition{
			Description:  input.Description,
			Name:         input.Name,
			DefaultValue: defaultValue,
			Type:         astType(input.Type),
			Directives:   deprecation(input.IsDeprecated, input.DeprecationReason),
		})
	}
	for _, value := range typ.EnumValues {
		definition.EnumValues = append(definition.EnumValues, &ast.EnumValueDefinition{
			Description: value.Description,
			Name:        value.Name,
			Directives:  deprecation(value.IsDeprecated, value.DeprecationReason),
		})
	}
	return definition, nil
}

func argumentDefinitions(values []introspectionInputValue) (ast.ArgumentDefinitionList, error) {
	var args ast.ArgumentDefinitionList
	for _, value := range values {
		defaultValue, err := literal(value.DefaultValue)
		if err != nil {
			return nil, fmt.Errorf("argument %s: %w", value.Name, err)
		}
		args = append(args, &ast.ArgumentDefinition{
			Description:  value.Description,
			Name:         value.Name,
			DefaultValue: defaultValue,
			Type:         astType(value.Type),
			Directives:   deprecation(value.IsDeprecated, value.DeprecationReason),
		})
	}
	return args, nil
}

// literal parses an introspection defaultValue, which the spec gives as a GraphQL literal such as
// `{page: 0, pageSize: 20}` or `ASC`, into the value the formatter prints. A nil default is no default.
func literal(defaultValue *string) (*ast.Value, error) {
	if defaultValue == nil {
		return nil, nil
	}
	query, err := parser.ParseQuery(&ast.Source{Input: "query($v: Int = " + *defaultValue + ") { v }"})
	if err != nil {
		return nil, fmt.Errorf("parse default value %q: %w", *defaultValue, err)
	}
	return query.Operations[0].VariableDefinitions[0].DefaultValue, nil
}

func deprecation(isDeprecated bool, reason *string) ast.DirectiveList {
	if !isDeprecated {
		return nil
	}
	directive := &ast.Directive{Name: "deprecated"}
	if reason != nil && *reason != "" {
		directive.Arguments = ast.ArgumentList{{Name: "reason", Value: &ast.Value{Kind: ast.StringValue, Raw: *reason}}}
	}
	return ast.DirectiveList{directive}
}

func astType(ref *introspectionTypeRef) *ast.Type {
	switch ref.Kind {
	case "NON_NULL":
		inner := astType(ref.OfType)
		inner.NonNull = true
		return inner
	case "LIST":
		return ast.ListType(astType(ref.OfType), nil)
	default:
		return ast.NamedType(ref.Name, nil)
	}
}

type introspectionSchema struct {
	Types      []introspectionType      `json:"types"`
	Directives []introspectionDirective `json:"directives"`
}

type introspectionDirective struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Locations   []string                  `json:"locations"`
	Args        []introspectionInputValue `json:"args"`
}

type introspectionType struct {
	Kind          string                    `json:"kind"`
	Name          string                    `json:"name"`
	Description   string                    `json:"description"`
	Fields        []introspectionField      `json:"fields"`
	InputFields   []introspectionInputValue `json:"inputFields"`
	Interfaces    []introspectionTypeRef    `json:"interfaces"`
	EnumValues    []introspectionEnumValue  `json:"enumValues"`
	PossibleTypes []introspectionTypeRef    `json:"possibleTypes"`
}

type introspectionField struct {
	Name              string                    `json:"name"`
	Description       string                    `json:"description"`
	Args              []introspectionInputValue `json:"args"`
	Type              *introspectionTypeRef     `json:"type"`
	IsDeprecated      bool                      `json:"isDeprecated"`
	DeprecationReason *string                   `json:"deprecationReason"`
}

type introspectionInputValue struct {
	Name              string                `json:"name"`
	Description       string                `json:"description"`
	Type              *introspectionTypeRef `json:"type"`
	DefaultValue      *string               `json:"defaultValue"`
	IsDeprecated      bool                  `json:"isDeprecated"`
	DeprecationReason *string               `json:"deprecationReason"`
}

type introspectionEnumValue struct {
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

type introspectionTypeRef struct {
	Kind   string                `json:"kind"`
	Name   string                `json:"name"`
	OfType *introspectionTypeRef `json:"ofType"`
}
