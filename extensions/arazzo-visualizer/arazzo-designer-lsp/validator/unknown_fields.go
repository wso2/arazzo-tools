package validator

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Known key sets for each Arazzo object type (v1.1.0). Specification extensions
// (keys beginning with "x-") are allowed everywhere and are not listed here.
// Reusable Object fields ("reference", "value") are folded into the parameter/action
// sets because those positions accept either an inline object or a Reusable Object.
var (
	rootKeys          = keySet("arazzo", "$self", "info", "sourceDescriptions", "workflows", "components")
	infoKeys          = keySet("title", "summary", "description", "version")
	sourceKeys        = keySet("name", "url", "type")
	workflowKeys      = keySet("workflowId", "summary", "description", "inputs", "parameters", "dependsOn", "steps", "successActions", "failureActions", "outputs")
	stepKeys          = keySet("stepId", "description", "operationId", "operationPath", "workflowId", "channelPath", "timeout", "correlationId", "action", "dependsOn", "parameters", "requestBody", "successCriteria", "onSuccess", "onFailure", "outputs")
	parameterKeys     = keySet("name", "in", "value", "reference")
	requestBodyKeys   = keySet("contentType", "payload", "replacements")
	replacementKeys   = keySet("target", "targetSelectorType", "value")
	criterionKeys     = keySet("context", "condition", "type")
	successActionKeys = keySet("name", "type", "stepId", "workflowId", "parameters", "criteria", "reference", "value")
	failureActionKeys = keySet("name", "type", "stepId", "workflowId", "retryAfter", "retryLimit", "parameters", "criteria", "reference", "value")
	componentsKeys    = keySet("inputs", "parameters", "successActions", "failureActions")
)

func keySet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// ValidateUnknownFields walks the raw YAML/JSON tree and warns about keys that are not part
// of the Arazzo v1.1.0 schema. This catches field-name typos (e.g. "chanelPath", "$ref" instead
// of "reference") that struct-based parsing silently ignores. Free-form regions (inputs schemas,
// outputs maps, request-body payloads, parameter values) are intentionally not descended into.
// Returns warnings only; a parse failure here is ignored because the main parser already reports it.
func (v *Validator) ValidateUnknownFields(content string) []ValidationError {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return nil
	}
	doc := documentRoot(&root)
	if doc == nil || doc.Kind != yaml.MappingNode {
		return nil
	}

	var errors []ValidationError
	add := func(errs []ValidationError) { errors = append(errors, errs...) }

	add(checkKeys(doc, rootKeys, "the Arazzo document"))
	forEachMapEntry(doc, func(key string, _, val *yaml.Node) {
		switch key {
		case "info":
			add(checkKeys(val, infoKeys, "info"))
		case "sourceDescriptions":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				add(checkKeys(item, sourceKeys, "a source description"))
			})
		case "workflows":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				add(checkWorkflow(item))
			})
		case "components":
			add(checkComponents(val))
		}
	})
	return errors
}

func checkWorkflow(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	errors = append(errors, checkKeys(node, workflowKeys, "a workflow")...)
	forEachMapEntry(node, func(key string, _, val *yaml.Node) {
		switch key {
		case "parameters":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, parameterKeys, "a parameter")...)
			})
		case "steps":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkStep(item)...)
			})
		case "successActions":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, successActionKeys, "a success action")...)
			})
		case "failureActions":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, failureActionKeys, "a failure action")...)
			})
		}
	})
	return errors
}

func checkStep(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	errors = append(errors, checkKeys(node, stepKeys, "a step")...)
	forEachMapEntry(node, func(key string, _, val *yaml.Node) {
		switch key {
		case "parameters":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, parameterKeys, "a parameter")...)
			})
		case "requestBody":
			errors = append(errors, checkRequestBody(val)...)
		case "successCriteria":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, criterionKeys, "a criterion")...)
			})
		case "onSuccess":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkAction(item, successActionKeys, "a success action")...)
			})
		case "onFailure":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkAction(item, failureActionKeys, "a failure action")...)
			})
		}
	})
	return errors
}

func checkRequestBody(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	errors = append(errors, checkKeys(node, requestBodyKeys, "requestBody")...)
	forEachMapEntry(node, func(key string, _, val *yaml.Node) {
		if key == "replacements" {
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, replacementKeys, "a payload replacement")...)
			})
		}
	})
	return errors
}

func checkAction(node *yaml.Node, allowed map[string]bool, label string) []ValidationError {
	var errors []ValidationError
	errors = append(errors, checkKeys(node, allowed, label)...)
	forEachMapEntry(node, func(key string, _, val *yaml.Node) {
		switch key {
		case "parameters":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, parameterKeys, "a parameter")...)
			})
		case "criteria":
			forEachSeqItem(val, func(_ int, item *yaml.Node) {
				errors = append(errors, checkKeys(item, criterionKeys, "a criterion")...)
			})
		}
	})
	return errors
}

// checkComponents validates the components section. Its parameters/successActions/failureActions
// are maps keyed by component name whose VALUES are the respective objects; inputs are free-form schemas.
func checkComponents(node *yaml.Node) []ValidationError {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	var errors []ValidationError
	errors = append(errors, checkKeys(node, componentsKeys, "components")...)
	forEachMapEntry(node, func(section string, _, val *yaml.Node) {
		switch section {
		case "parameters":
			forEachMapEntry(val, func(_ string, _, obj *yaml.Node) {
				errors = append(errors, checkKeys(obj, parameterKeys, "a component parameter")...)
			})
		case "successActions":
			forEachMapEntry(val, func(_ string, _, obj *yaml.Node) {
				errors = append(errors, checkAction(obj, successActionKeys, "a component success action")...)
			})
		case "failureActions":
			forEachMapEntry(val, func(_ string, _, obj *yaml.Node) {
				errors = append(errors, checkAction(obj, failureActionKeys, "a component failure action")...)
			})
		}
	})
	return errors
}

// checkKeys reports a warning for every key in a mapping node that is neither in the allowed set
// nor a specification extension ("x-" prefix).
func checkKeys(node *yaml.Node, allowed map[string]bool, objectLabel string) []ValidationError {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	var errors []ValidationError
	forEachMapEntry(node, func(key string, keyNode, _ *yaml.Node) {
		if allowed[key] || strings.HasPrefix(key, "x-") {
			return
		}
		errors = append(errors, ValidationError{
			Line:     keyNode.Line - 1, // yaml.Node.Line is 1-based; LSP ranges are 0-based
			Column:   0,
			Message:  fmt.Sprintf("Unknown field '%s' in %s (not part of the Arazzo v1.1.0 schema)", key, objectLabel),
			Severity: "warning",
		})
	})
	return errors
}

// documentRoot unwraps a yaml DocumentNode to its content mapping.
func documentRoot(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		return node.Content[0]
	}
	return node
}

// forEachMapEntry iterates key/value pairs of a mapping node (no-op for non-mappings).
func forEachMapEntry(node *yaml.Node, fn func(key string, keyNode, valNode *yaml.Node)) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		fn(node.Content[i].Value, node.Content[i], node.Content[i+1])
	}
}

// forEachSeqItem iterates items of a sequence node (no-op for non-sequences).
func forEachSeqItem(node *yaml.Node, fn func(index int, item *yaml.Node)) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return
	}
	for i, item := range node.Content {
		fn(i, item)
	}
}
