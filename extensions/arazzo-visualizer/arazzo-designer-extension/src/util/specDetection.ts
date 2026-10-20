/**
 * Copyright (c) 2025, WSO2 LLC. (https://www.wso2.com) All Rights Reserved.
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

/**
 * What a document says it is, read from its content rather than its file name. Shared by the editor,
 * which uses it to attach the Arazzo language and show the play button, and by the server start, so
 * every file the editor offers to run is a file the server will accept.
 */
export function detectSpecType(text: string): { isArazzo: boolean; isOpenAPI: boolean } {
	// Content-based detection on the REQUIRED root field: `arazzo` for an Arazzo Description (spec
	// v1.1.0 §5.8.1.1: "The `arazzo` field MUST be used by tooling to interpret the Arazzo
	// Description"), `openapi` for an OpenAPI document. The spec does not require the field to come
	// FIRST - reading only the first meaningful line is this tool's own trade-off, which misses a valid
	// document that puts another key first (accepted by design, see asyncapi_plan.md). Detect on the
	// TOPMOST non-blank, non-comment line ONLY — this both (a) allows a comment header of any length
	// before the field (v1.1.0 examples
	// commonly have one, which the old first-10-lines check missed) and (b) avoids a false positive
	// from a stray `arazzo:`/`openapi:` appearing deeper in the file (in a description, a comment, or
	// a source-description name). `---` YAML document markers are skipped. An optional quote around
	// the version is allowed so `arazzo: "1.1.0"` matches as well as `arazzo: 1.1.0`.
	//
	// JSON documents open with a bare `{`, so that line is skipped too and the key itself may be
	// quoted — otherwise `{ "arazzo": "1.1.0" }` would never be recognised and the arazzo-json
	// language (and with it the language server) would not attach.
	const firstMeaningfulLine = text
		.split(/\r?\n/)
		.find(l => {
			const t = l.trim();
			return t !== '' && t !== '---' && t !== '{' && !t.startsWith('#');
		}) || '';
	// The optional leading `{` covers compact JSON, where the opening brace and the root key share a
	// line (`{"arazzo":"1.1.0",…}`) and so the brace-only skip above does not apply.
	const hasOpenAPI = /^\s*\{?\s*"?openapi"?\s*:/i.test(firstMeaningfulLine);
	const hasArazzo = /^\s*\{?\s*"?arazzo"?\s*:\s*["']?\d+\.\d+\.\d+/i.test(firstMeaningfulLine);

	return { isArazzo: hasArazzo, isOpenAPI: hasOpenAPI && !hasArazzo };
}
