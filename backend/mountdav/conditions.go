package mountdav

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const maxConditionalHeaderBytes = 16 << 10

func parseMutationConditions(
	request *http.Request,
	requestPath string,
	resolveTaggedResource func(string) (string, int),
) (MutationConditions, int) {
	ifMatch, ok := parseETagConditions(request.Header.Values("If-Match"))
	if !ok {
		return MutationConditions{}, http.StatusBadRequest
	}
	ifNoneMatch, ok := parseETagConditions(request.Header.Values("If-None-Match"))
	if !ok {
		return MutationConditions{}, http.StatusBadRequest
	}
	result := MutationConditions{IfMatch: ifMatch, IfNoneMatch: ifNoneMatch}

	values := request.Header.Values("If")
	if len(values) == 0 {
		return result, 0
	}
	value := strings.Join(values, " ")
	if len(value) > maxConditionalHeaderBytes {
		return MutationConditions{}, http.StatusRequestHeaderFieldsTooLarge
	}
	lists, ok := parseDAVIf(value)
	if !ok {
		return MutationConditions{}, http.StatusBadRequest
	}

	seenTokens := make(map[string]struct{})
	result.DAVIf = make([]DAVConditionList, 0, len(lists))
	for _, list := range lists {
		resourcePath := requestPath
		if list.resourceTag != "" {
			var status int
			resourcePath, status = resolveTaggedResource(list.resourceTag)
			if status != 0 {
				return MutationConditions{}, status
			}
		}
		conditions := make([]DAVCondition, len(list.conditions))
		for index, condition := range list.conditions {
			conditions[index] = condition
			if condition.Not || condition.LockToken == "" {
				continue
			}
			if _, exists := seenTokens[condition.LockToken]; exists {
				continue
			}
			seenTokens[condition.LockToken] = struct{}{}
			result.LockTokens = append(result.LockTokens, condition.LockToken)
		}
		result.DAVIf = append(result.DAVIf, DAVConditionList{
			ResourcePath: resourcePath,
			Conditions:   conditions,
		})
	}
	return result, 0
}

func parseETagConditions(values []string) (ETagConditions, bool) {
	if len(values) == 0 {
		return ETagConditions{}, true
	}
	input := strings.TrimSpace(strings.Join(values, ","))
	if input == "" {
		return ETagConditions{}, false
	}
	if input == "*" {
		return ETagConditions{Present: true, Any: true}, true
	}

	tags := make([]EntityTag, 0, 2)
	for len(input) > 0 {
		input = strings.TrimSpace(input)
		tag, remaining, ok := consumeEntityTag(input)
		if !ok {
			return ETagConditions{}, false
		}
		tags = append(tags, tag)
		remaining = strings.TrimSpace(remaining)
		if remaining == "" {
			break
		}
		if remaining[0] != ',' {
			return ETagConditions{}, false
		}
		input = remaining[1:]
		if strings.TrimSpace(input) == "" {
			return ETagConditions{}, false
		}
	}
	return ETagConditions{Present: true, Tags: tags}, len(tags) > 0
}

func consumeEntityTag(input string) (EntityTag, string, bool) {
	weak := false
	if strings.HasPrefix(input, "W/") {
		weak = true
		input = input[2:]
	}
	if len(input) < 2 || input[0] != '"' {
		return EntityTag{}, "", false
	}
	end := strings.IndexByte(input[1:], '"')
	if end < 0 {
		return EntityTag{}, "", false
	}
	end++
	opaque := input[1:end]
	for _, character := range opaque {
		if character == 0x7f || character < 0x21 || character == '"' {
			return EntityTag{}, "", false
		}
	}
	return EntityTag{Weak: weak, Opaque: opaque}, input[end+1:], true
}

func validStrongETag(value string) bool {
	tag, remaining, ok := consumeEntityTag(strings.TrimSpace(value))
	return ok && !tag.Weak && strings.TrimSpace(remaining) == ""
}

type parsedDAVIfList struct {
	resourceTag string
	conditions  []DAVCondition
}

func parseDAVIf(value string) ([]parsedDAVIfList, bool) {
	lexer := davIfLexer{input: strings.TrimSpace(value)}
	first := lexer.peek()
	switch first.kind {
	case davTokenLeftParen:
		return parseUntaggedDAVLists(&lexer)
	case davTokenAngle:
		return parseTaggedDAVLists(&lexer)
	default:
		return nil, false
	}
}

func parseUntaggedDAVLists(lexer *davIfLexer) ([]parsedDAVIfList, bool) {
	var lists []parsedDAVIfList
	for lexer.peek().kind != davTokenEOF {
		list, ok := parseDAVList(lexer)
		if !ok {
			return nil, false
		}
		lists = append(lists, list)
	}
	return lists, len(lists) > 0
}

func parseTaggedDAVLists(lexer *davIfLexer) ([]parsedDAVIfList, bool) {
	var lists []parsedDAVIfList
	for lexer.peek().kind != davTokenEOF {
		resource := lexer.next()
		if resource.kind != davTokenAngle || !validStateURI(resource.value) {
			return nil, false
		}
		listCount := 0
		for lexer.peek().kind == davTokenLeftParen {
			list, ok := parseDAVList(lexer)
			if !ok {
				return nil, false
			}
			list.resourceTag = resource.value
			lists = append(lists, list)
			listCount++
		}
		if listCount == 0 {
			return nil, false
		}
	}
	return lists, len(lists) > 0
}

func parseDAVList(lexer *davIfLexer) (parsedDAVIfList, bool) {
	if lexer.next().kind != davTokenLeftParen {
		return parsedDAVIfList{}, false
	}
	var conditions []DAVCondition
	for {
		if lexer.peek().kind == davTokenRightParen {
			lexer.next()
			return parsedDAVIfList{conditions: conditions}, len(conditions) > 0
		}
		condition, ok := parseDAVCondition(lexer)
		if !ok {
			return parsedDAVIfList{}, false
		}
		conditions = append(conditions, condition)
	}
}

func parseDAVCondition(lexer *davIfLexer) (DAVCondition, bool) {
	condition := DAVCondition{}
	if lexer.peek().kind == davTokenNot {
		lexer.next()
		condition.Not = true
	}
	token := lexer.next()
	switch token.kind {
	case davTokenAngle:
		if !validStateURI(token.value) {
			return DAVCondition{}, false
		}
		condition.LockToken = token.value
	case davTokenSquare:
		tag, remaining, ok := consumeEntityTag(strings.TrimSpace(token.value))
		if !ok || strings.TrimSpace(remaining) != "" {
			return DAVCondition{}, false
		}
		condition.ETag = &tag
	default:
		return DAVCondition{}, false
	}
	return condition, true
}

func validStateURI(value string) bool {
	if value == "" || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.Fragment == ""
}

type davTokenKind uint8

const (
	davTokenInvalid davTokenKind = iota
	davTokenEOF
	davTokenLeftParen
	davTokenRightParen
	davTokenAngle
	davTokenSquare
	davTokenNot
)

type davToken struct {
	kind  davTokenKind
	value string
}

type davIfLexer struct {
	input  string
	peeked *davToken
}

func (lexer *davIfLexer) peek() davToken {
	if lexer.peeked == nil {
		token := lexer.scan()
		lexer.peeked = &token
	}
	return *lexer.peeked
}

func (lexer *davIfLexer) next() davToken {
	if lexer.peeked != nil {
		token := *lexer.peeked
		lexer.peeked = nil
		return token
	}
	return lexer.scan()
}

func (lexer *davIfLexer) scan() davToken {
	lexer.input = strings.TrimLeft(lexer.input, " \t")
	if lexer.input == "" {
		return davToken{kind: davTokenEOF}
	}
	switch lexer.input[0] {
	case '(':
		lexer.input = lexer.input[1:]
		return davToken{kind: davTokenLeftParen}
	case ')':
		lexer.input = lexer.input[1:]
		return davToken{kind: davTokenRightParen}
	case '<':
		return lexer.delimited('>', davTokenAngle)
	case '[':
		return lexer.delimited(']', davTokenSquare)
	}

	end := strings.IndexAny(lexer.input, " \t()<>[]")
	if end < 0 {
		end = len(lexer.input)
	}
	word := lexer.input[:end]
	lexer.input = lexer.input[end:]
	if word == "Not" {
		return davToken{kind: davTokenNot}
	}
	return davToken{kind: davTokenInvalid}
}

func (lexer *davIfLexer) delimited(end byte, kind davTokenKind) davToken {
	index := strings.IndexByte(lexer.input[1:], end)
	if index < 0 {
		lexer.input = ""
		return davToken{kind: davTokenInvalid}
	}
	index++
	value := lexer.input[1:index]
	lexer.input = lexer.input[index+1:]
	return davToken{kind: kind, value: value}
}
