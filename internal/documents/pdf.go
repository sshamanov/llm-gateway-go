package documents

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// --------------------------------------------------------------------------
// Public API
// --------------------------------------------------------------------------

// ExtractPDFText opens the PDF at path and returns extracted text, one string
// per page. Returns ErrPDFNoText if no text could be extracted.
func ExtractPDFText(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading PDF: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrEmptyFile
	}

	xref, err := parseXref(data)
	if err != nil {
		return nil, fmt.Errorf("parsing xref table: %w", err)
	}

	trailer, err := parseTrailer(data, xref)
	if err != nil {
		return nil, fmt.Errorf("parsing trailer: %w", err)
	}

	rootRef, ok := trailer["/Root"]
	if !ok {
		return nil, fmt.Errorf("trailer missing /Root")
	}

	// Check for encryption before descending.
	encRef, hasEnc := trailer["/Encrypt"]
	if hasEnc && encRef != "" {
		return nil, ErrEncryptedPDF
	}

	pageRefs, err := collectPageRefs(data, xref, rootRef)
	if err != nil {
		return nil, fmt.Errorf("collecting page references: %w", err)
	}

	if len(pageRefs) == 0 {
		return nil, fmt.Errorf("no pages found in PDF")
	}

	pages := make([]string, 0, len(pageRefs))
	anyText := false

	for _, pageRef := range pageRefs {
		text, err := extractPageText(data, xref, pageRef)
		if err != nil {
			// If the encryption check fails on an individual page, return it.
			if err == ErrEncryptedPDF {
				return nil, err
			}
			// Otherwise skip the page silently.
			text = ""
		}
		if strings.TrimSpace(text) != "" {
			anyText = true
		}
		pages = append(pages, text)
	}

	if !anyText {
		return nil, ErrPDFNoText
	}
	return pages, nil
}

// --------------------------------------------------------------------------
// Cross-reference table
// --------------------------------------------------------------------------

// parseXref finds and parses the cross-reference table. It returns a map of
// object number -> byte offset.
func parseXref(data []byte) (map[int]int64, error) {
	// Scan backwards for the last occurrence of "startxref".
	startXrefPos := bytes.LastIndex(data, []byte("startxref"))
	if startXrefPos < 0 {
		return nil, fmt.Errorf("startxref not found")
	}

	// The integer right after "startxref".
	rest := data[startXrefPos+len("startxref"):]
	rest = trimLeftSpace(rest)

	var xrefOffset int64
	_, err := fmt.Sscanf(string(rest), "%d", &xrefOffset)
	if err != nil {
		return nil, fmt.Errorf("parsing startxref offset: %w", err)
	}

	xref := make(map[int]int64)

	// Check if the offset points to a cross-reference stream (PDF 1.5+) or a
	// classic xref table. Classic tables start with "xref" on its own line.
	line := trimLeftSpace(data[xrefOffset:])
	if bytes.HasPrefix(line, []byte("xref")) {
		if err := parseClassicXref(data, xrefOffset, xref); err != nil {
			return nil, err
		}
	} else {
		// PDF 1.5+ cross-reference stream.
		if err := parseXrefStream(data, xrefOffset, xref); err != nil {
			// Fall back to trying classic xref table by searching for it.
			if xrefPos := bytes.Index(data, []byte("\nxref")); xrefPos >= 0 {
				xrefOfs := int64(xrefPos)
				for xrefOfs > 0 && data[xrefOfs-1] == '\n' {
					xrefOfs--
				}
				if err := parseClassicXref(data, xrefOfs+1, xref); err != nil {
					return nil, fmt.Errorf("fallback classic xref: %w", err)
				}
				return xref, nil
			}
			return nil, fmt.Errorf("parsing xref stream: %w (no classic xref table found either)", err)
		}
	}

	return xref, nil
}

// parseClassicXref parses the classic xref table starting at the given offset.
func parseClassicXref(data []byte, offset int64, xref map[int]int64) error {
	pos := int(offset)
	line := nextLine(data, &pos)

	// Expect "xref".
	if string(bytes.TrimSpace(line)) != "xref" {
		return fmt.Errorf("expected 'xref' at offset %d", offset)
	}

	for pos < len(data) {
		line = trimLeftSpace(data[pos:])
		if len(line) == 0 {
			break
		}

		// Check for sub-section header like "0 6"
		parts := bytes.Fields(line)
		if len(parts) != 2 {
			break
		}
		startObj, err1 := strconv.Atoi(string(parts[0]))
		count, err2 := strconv.Atoi(string(parts[1]))
		if err1 != nil || err2 != nil {
			break
		}

		// Skip past the sub-section header line.
		advanceLine(data, &pos)

		for i := 0; i < count; i++ {
			line := nextLine(data, &pos)
			if len(line) == 0 {
				return fmt.Errorf("unexpected end of xref table")
			}
			// Format: nnnnnnnnnn ggggg n
			// Fields: byte offset (10 digits), generation (5 digits), "n" or "f".
			if len(line) < 20 {
				// Skip malformed entry.
				continue
			}
			offsetStr := string(bytes.TrimSpace(line[:11]))
			inUse := len(line) >= 20 && line[17] == 'n'

			if inUse {
				objOffset, err := strconv.ParseInt(offsetStr, 10, 64)
				if err == nil {
					xref[startObj+i] = objOffset
				}
			}
		}

		// The next line may be "trailer" or another sub-section header.
		// Peek ahead without consuming -- trimLeftSpace is non-destructive.
		peek := trimLeftSpace(data[pos:])
		if bytes.HasPrefix(peek, []byte("trailer")) {
			break
		}
	}

	return nil
}

// parseXrefStream parses a PDF 1.5+ cross-reference stream object.
func parseXrefStream(data []byte, offset int64, xref map[int]int64) error {
	// Try to parse this as an indirect object containing a cross-reference stream.
	dict, streamData, err := parseObjectAtOffset(data, offset)
	if err != nil {
		return err
	}

	typ := dict["/Type"]
	if typ != "" && typ != "/XRef" {
		return fmt.Errorf("object at offset %d has /Type %s, not /XRef", offset, typ)
	}

	// If the xref stream is itself compressed with FlateDecode, decompress it.
	filter := dict["/Filter"]
	if filter == "/FlateDecode" && len(streamData) > 0 {
		// Fields that tell us the xref record structure.
		wStr := dict["/W"]
		if wStr == "" {
			return fmt.Errorf("xref stream missing /W array")
		}
		indexStr := dict["/Index"]
		_ = indexStr // optional; if missing, assume [0 /Size]

		// Parse /W array: [field1_bytes field2_bytes field3_bytes]
		wParts := parsePDFArray(wStr)
		if len(wParts) < 3 {
			return fmt.Errorf("invalid /W array in xref stream")
		}

		// Parse /Index. Default is [0 /Size].
		var indexParts []string
		if indexStr != "" {
			indexParts = parsePDFArray(indexStr)
		} else {
			sizeStr := dict["/Size"]
			indexParts = []string{"0", sizeStr}
		}

		for k := 0; k+1 < len(indexParts); k += 2 {
			firstObj, _ := strconv.Atoi(indexParts[k])
			numObjs, _ := strconv.Atoi(indexParts[k+1])

			f1, _ := strconv.Atoi(wParts[0])
			f2, _ := strconv.Atoi(wParts[1])
			f3, _ := strconv.Atoi(wParts[2])

			entrySize := f1 + f2 + f3
			if entrySize == 0 {
				continue
			}

			rd, err := zlib.NewReader(bytes.NewReader(streamData))
			if err != nil {
				return fmt.Errorf("creating zlib reader for xref stream: %w", err)
			}
			defer rd.Close()

			decoded, err := io.ReadAll(rd)
			if err != nil {
				return fmt.Errorf("decompressing xref stream: %w", err)
			}

			for i := 0; i < numObjs; i++ {
				if (i+1)*entrySize > len(decoded) {
					break
				}
				entry := decoded[i*entrySize : (i+1)*entrySize]
				var typByte byte
				if f1 > 0 {
					typByte = entry[0]
				}
				var field2 int64
				if f2 > 0 {
					field2 = parseVarInt(entry[f1 : f1+f2])
				}
				var field3 int64
				if f3 > 0 {
					field3 = parseVarInt(entry[f1+f2 : f1+f2+f3])
				}
				_ = field3 // field3 is the object index for compressed objects

				objNum := firstObj + i
				switch typByte {
				case 1: // in-use object
					xref[objNum] = field2
				case 2: // compressed object (in object stream)
					xref[objNum] = field2 // field2 is object stream number
				}
			}
		}

		return nil
	}

	// If we can't handle it, the xref map stays empty and we'll try
	// alternative approaches in the caller.
	return fmt.Errorf("xref stream has unsupported filter or no stream data")
}

// parseVarInt reads a variable-length big-endian integer from b.
func parseVarInt(b []byte) int64 {
	var v int64
	for _, bb := range b {
		v = (v << 8) | int64(bb)
	}
	return v
}

// parseObjectAtOffset reads an indirect object at the given byte offset.
// Returns the dictionary map and the raw stream bytes (if any).
func parseObjectAtOffset(data []byte, offset int64) (map[string]string, []byte, error) {
	_, _, dict, stream, err := parseObject(data, offset)
	return dict, stream, err
}

// --------------------------------------------------------------------------
// Trailer
// --------------------------------------------------------------------------

// parseTrailer extracts the trailer dictionary from after the xref table.
// It uses the xref entries to find /Root etc. but we actually parse the
// trailer text directly from the file.
func parseTrailer(data []byte, xref map[int]int64) (map[string]string, error) {
	// Locate "trailer" keyword after the xref table.
	// We scan from where xref could be.
	trailerPos := bytes.LastIndex(data, []byte("\ntrailer"))
	if trailerPos < 0 {
		trailerPos = bytes.LastIndex(data, []byte("trailer"))
	}
	if trailerPos < 0 {
		return nil, fmt.Errorf("trailer keyword not found")
	}

	// Move past "trailer" to find <<...>>
	pos := trailerPos + len("trailer")
	rest := data[pos:]
	dictStart := bytes.Index(rest, []byte("<<"))
	if dictStart < 0 {
		return nil, fmt.Errorf("trailer dictionary not found")
	}

	dict, _, err := parseDict(data, pos+dictStart)
	if err != nil {
		return nil, fmt.Errorf("parsing trailer dict: %w", err)
	}
	return dict, nil
}

// --------------------------------------------------------------------------
// Catalog / Pages tree
// --------------------------------------------------------------------------

// collectPageRefs walks the page tree starting from Root and returns all page
// object references found.
func collectPageRefs(data []byte, xref map[int]int64, rootRef string) ([]string, error) {
	rootDict, _, err := resolveRef(data, rootRef, xref)
	if err != nil {
		// Try to find the root directly from the root object number.
		objNum, _, _ := parseRef(rootRef)
		if objNum < 0 {
			return nil, fmt.Errorf("resolving root reference %s: %w", rootRef, err)
		}
		_, _, rootDict, _, err = parseObject(data, xref[objNum])
		if err != nil {
			return nil, fmt.Errorf("resolving root object %d: %w", objNum, err)
		}
	}

	pagesRef, ok := rootDict["/Pages"]
	if !ok {
		return nil, fmt.Errorf("catalog missing /Pages")
	}

	var pageRefs []string
	if err := walkPages(data, xref, pagesRef, &pageRefs); err != nil {
		return nil, err
	}
	return pageRefs, nil
}

// walkPages recursively walks the page tree.
func walkPages(data []byte, xref map[int]int64, nodeRef string, acc *[]string) error {
	nodeDict, _, err := resolveRef(data, nodeRef, xref)
	if err != nil {
		// Direct offset lookup fallback.
		objNum, _, _ := parseRef(nodeRef)
		if objNum < 0 {
			return fmt.Errorf("resolving node %s: %w", nodeRef, err)
		}
		off, ok := xref[objNum]
		if !ok {
			return fmt.Errorf("object %d not in xref table", objNum)
		}
		_, _, nodeDict, _, err = parseObject(data, off)
		if err != nil {
			return fmt.Errorf("parsing node object %d: %w", objNum, err)
		}
	}

	typ := nodeDict["/Type"]
	kids := nodeDict["/Kids"]

	// If no /Type specified, check for /Kids to infer type.
	if typ == "" && kids != "" {
		// Assume it's a Pages node.
		typ = "/Pages"
	}

	if typ == "/Pages" {
		if kids == "" {
			// Might have a /Count of 0 or be a leaf, treat as page.
			*acc = append(*acc, nodeRef)
			return nil
		}
		// Parse kid references.
		kidRefs := parsePDFArray(kids)
		for _, kidRef := range kidRefs {
			if err := walkPages(data, xref, kidRef, acc); err != nil {
				return err
			}
		}
		return nil
	}

	// Assume it's a Page object.
	*acc = append(*acc, nodeRef)
	return nil
}

// --------------------------------------------------------------------------
// Page text extraction
// --------------------------------------------------------------------------

// extractPageText extracts all text from a single page object.
func extractPageText(data []byte, xref map[int]int64, pageRef string) (string, error) {
	pageDict, _, err := resolveRef(data, pageRef, xref)
	if err != nil {
		objNum, _, _ := parseRef(pageRef)
		if objNum < 0 {
			return "", fmt.Errorf("resolving page %s: %w", pageRef, err)
		}
		off, ok := xref[objNum]
		if !ok {
			return "", fmt.Errorf("page object %d not in xref table", objNum)
		}
		_, _, pageDict, _, err = parseObject(data, off)
		if err != nil {
			return "", fmt.Errorf("parsing page object %d: %w", objNum, err)
		}
	}

	// Check for encryption on this page.
	if _, ok := pageDict["/Encrypt"]; ok {
		return "", ErrEncryptedPDF
	}

	contents := pageDict["/Contents"]
	if contents == "" {
		return "", nil
	}

	var streamRefs []string
	if strings.HasPrefix(contents, "[") {
		// Array of references.
		streamRefs = parsePDFArray(contents)
	} else {
		// Single reference.
		streamRefs = []string{contents}
	}

	var pageText strings.Builder
	for _, ref := range streamRefs {
		_, streamData, err := resolveRef(data, ref, xref)
		if err != nil {
			objNum, _, _ := parseRef(ref)
			if objNum < 0 {
				continue
			}
			if off, ok := xref[objNum]; ok {
				_, _, _, streamData, err = parseObject(data, off)
				if err != nil {
					continue
				}
			} else {
				continue
			}
		}

		text := extractTextFromStream(streamData)
		if pageText.Len() > 0 && text != "" {
			pageText.WriteString(" ")
		}
		pageText.WriteString(text)
	}

	return pageText.String(), nil
}

// --------------------------------------------------------------------------
// Object parsing
// --------------------------------------------------------------------------

// parseObject reads an indirect object at the given byte offset.
// Returns object number, generation number, dictionary map, stream data, error.
func parseObject(data []byte, offset int64) (int, int, map[string]string, []byte, error) {
	if offset < 0 || int(offset) >= len(data) {
		return 0, 0, nil, nil, fmt.Errorf("offset %d out of range", offset)
	}

	pos := int(offset)
	line := nextLine(data, &pos)
	parts := bytes.Fields(line)
	if len(parts) < 3 {
		return 0, 0, nil, nil, fmt.Errorf("invalid object header at offset %d", offset)
	}

	objNum, err := strconv.Atoi(string(parts[0]))
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("invalid object number at offset %d: %w", offset, err)
	}
	genNum, err := strconv.Atoi(string(parts[1]))
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("invalid generation number at offset %d: %w", offset, err)
	}
	// The line should end with "obj".
	if string(parts[2]) != "obj" {
		return 0, 0, nil, nil, fmt.Errorf("expected 'obj' at offset %d, got %s", offset, parts[2])
	}

	// Now parse the object body. Skip whitespace.
	skipWhitespace(data, &pos)

	// Check for dictionary.
	var dict map[string]string
	if pos < len(data) && data[pos] == '<' && pos+1 < len(data) && data[pos+1] == '<' {
		var end int
		dict, end, err = parseDict(data, pos)
		if err != nil {
			return objNum, genNum, nil, nil, fmt.Errorf("parsing dict in object %d: %w", objNum, err)
		}
		pos = end
		skipWhitespace(data, &pos)
	}

	// Check for stream keyword.
	var streamData []byte
	if pos+6 <= len(data) && string(data[pos:pos+6]) == "stream" {
		streamStart := findStreamStart(data, pos)
		if streamStart >= 0 {
			streamEnd := findStreamEnd(data, streamStart)
			if streamEnd > streamStart {
				streamData = data[streamStart:streamEnd]
			}

			// Check Filter in dict for FlateDecode.
			if dict != nil && len(streamData) > 0 {
				if dict["/Filter"] == "/FlateDecode" {
					decompressed, err := decompressFlate(streamData)
					if err == nil {
						streamData = decompressed
					}
				}
			}
		}
	}

	return objNum, genNum, dict, streamData, nil
}

// parseDict extracts key-value pairs from a PDF dictionary <<...>> at the given
// position in data. Returns a map of string keys to raw string values (including
// references like "1 0 R").
func parseDict(data []byte, start int) (map[string]string, int, error) {
	if start >= len(data) || data[start] != '<' || start+1 >= len(data) || data[start+1] != '<' {
		return nil, start, fmt.Errorf("parseDict: no '<<' at position %d", start)
	}

	pos := start + 2
	depth := 1
	dict := make(map[string]string)

	// First, extract the raw dictionary content between << and >>.
	// Track balanced << >>.
	rawEnd := -1
	for i := pos; i < len(data)-1; i++ {
		if data[i] == '<' && data[i+1] == '<' {
			depth++
			i++
		} else if data[i] == '>' && data[i+1] == '>' {
			depth--
			if depth == 0 {
				rawEnd = i
				break
			}
			i++
		}
	}

	if rawEnd < 0 {
		return nil, start, fmt.Errorf("parseDict: unmatched '<<'")
	}

	inner := data[pos:rawEnd]

	// Parse key-value pairs from the inner content.
	// Keys start with '/', values follow until the next '/' or end.
	i := 0
	for i < len(inner) {
		// Skip whitespace.
		for i < len(inner) && isSpace(inner[i]) {
			i++
		}
		if i >= len(inner) {
			break
		}
		if inner[i] != '/' {
			break
		}

		// Read key (starting with /).
		keyStart := i
		i++
		for i < len(inner) && !isSpace(inner[i]) && inner[i] != '/' && inner[i] != '>' {
			i++
		}
		key := string(inner[keyStart:i])

		// Skip whitespace to value.
		for i < len(inner) && isSpace(inner[i]) {
			i++
		}
		if i >= len(inner) {
			dict[key] = ""
			break
		}

		// If the next char is '/', this is a boolean / no value.
		if inner[i] == '/' {
			dict[key] = ""
			continue
		}
		// If next chars are '>>', we're done.
		if i+1 < len(inner) && inner[i] == '>' && inner[i+1] == '>' {
			dict[key] = ""
			break
		}

		// Read value.
		val, newPos := readValue(inner, i)
		dict[key] = val
		i = newPos
	}

	return dict, rawEnd + 2, nil
}

// readValue reads a PDF value starting at position i.
// Returns the value string and the new position.
func readValue(data []byte, i int) (string, int) {
	if i >= len(data) {
		return "", i
	}

	// String literal: ( ... )
	if data[i] == '(' {
		return readPDFString(data, i)
	}

	// Hex string: < ... >
	if data[i] == '<' {
		// Check for dict start <<
		if i+1 < len(data) && data[i+1] == '<' {
			// Nested dict -- skip to matching >>
			depth := 1
			j := i + 2
			for j < len(data)-1 {
				if data[j] == '<' && data[j+1] == '<' {
					depth++
					j++
				} else if data[j] == '>' && data[j+1] == '>' {
					depth--
					if depth == 0 {
						return string(data[i : j+2]), j + 2
					}
					j++
				}
				j++
			}
			return string(data[i:]), len(data)
		}
		// Hex string <...>
		j := i + 1
		for j < len(data) && data[j] != '>' {
			j++
		}
		if j < len(data) {
			j++ // include closing >
		}
		return string(data[i:j]), j
	}

	// Array: [ ... ]
	if data[i] == '[' {
		depth := 1
		j := i + 1
		for j < len(data) && depth > 0 {
			if data[j] == '[' {
				depth++
			} else if data[j] == ']' {
				depth--
			} else if data[j] == '(' {
				// Skip string literals within array.
				_, j = readPDFString(data, j)
				continue
			}
			j++
		}
		return string(data[i:j]), j
	}

	// Name: /...
	if data[i] == '/' {
		j := i + 1
		for j < len(data) && !isSpace(data[j]) && data[j] != '/' && data[j] != '>' && data[j] != ']' {
			j++
		}
		return string(data[i:j]), j
	}

	// Number or reference: digits, possibly with decimal point.
	if data[i] == '-' || data[i] == '+' || (data[i] >= '0' && data[i] <= '9') {
		j := i
		for j < len(data) && (data[j] == '-' || data[j] == '+' || data[j] == '.' || (data[j] >= '0' && data[j] <= '9')) {
			j++
		}
		// Check for reference pattern: "N M R"
		// Skip whitespace after number.
		tempJ := j
		for tempJ < len(data) && isSpace(data[tempJ]) {
			tempJ++
		}
		// Read next token.
		tokStart := tempJ
		for tempJ < len(data) && !isSpace(data[tempJ]) && data[tempJ] != ']' && data[tempJ] != '>' {
			tempJ++
		}
		nextTok := string(data[tokStart:tempJ])
		if nextTok == "R" || nextTok == "obj" || nextTok == "0" || nextTok == "1" {
			// Need to check if this is "N gen R" pattern.
			// We have the first number from i to j. After whitespace, we
			// found a token. If there's yet another token "R" after that,
			// it's a reference.
			j2 := tempJ
			for j2 < len(data) && isSpace(data[j2]) {
				j2++
			}
			tokStart2 := j2
			for j2 < len(data) && !isSpace(data[j2]) && data[j2] != ']' && data[j2] != '>' {
				j2++
			}
			nextTok2 := string(data[tokStart2:j2])
			if nextTok2 == "R" {
				return string(data[i:j2]), j2
			}
			if nextTok == "0" {
				_ = nextTok2
			}
		}
		return string(data[i:j]), j
	}

	// Boolean or keyword: true, false, null.
	j := i
	for j < len(data) && !isSpace(data[j]) && data[j] != '>' && data[j] != ']' {
		j++
	}
	return string(data[i:j]), j
}

// readPDFString reads a PDF string literal enclosed in parentheses, handling
// balanced nesting and escape sequences. Returns the raw string (with escapes)
// and the position after the closing ')'.
func readPDFString(data []byte, start int) (string, int) {
	if start >= len(data) || data[start] != '(' {
		return "", start
	}

	depth := 1
	i := start + 1
	for i < len(data) && depth > 0 {
		ch := data[i]
		if ch == '\\' {
			// Skip escaped character.
			i += 2
			continue
		}
		if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
		}
		if depth > 0 {
			i++
		}
	}
	if i >= len(data) {
		return string(data[start:]), len(data)
	}
	return string(data[start : i+1]), i + 1
}

// --------------------------------------------------------------------------
// Resolve references
// --------------------------------------------------------------------------

// resolveRef follows a reference string like "10 0 R" through the xref table
// to get the object's byte offset, then returns the parsed object dictionary
// and stream data.
func resolveRef(data []byte, ref string, xref map[int]int64) (map[string]string, []byte, error) {
	ref = strings.TrimSpace(ref)
	objNum, genNum, err := parseRef(ref)
	if err != nil {
		return nil, nil, err
	}

	offset, ok := xref[objNum]
	if !ok {
		return nil, nil, fmt.Errorf("object %d not found in xref table", objNum)
	}

	oNum, gNum, dict, stream, err := parseObject(data, offset)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing object %d: %w", objNum, err)
	}
	if oNum != objNum {
		return nil, nil, fmt.Errorf("expected object %d at offset %d, got %d", objNum, offset, oNum)
	}
	if genNum >= 0 && gNum != genNum {
		// Generation mismatch is okay in many PDFs -- just warn.
		_ = gNum
	}

	return dict, stream, nil
}

// parseRef parses a reference string like "10 0 R" into (10, 0, nil).
// Returns (-1, -1, error) on failure.
func parseRef(ref string) (int, int, error) {
	ref = strings.TrimSpace(ref)
	parts := strings.Fields(ref)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid reference: %q", ref)
	}

	objNum, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid object number in reference %q: %w", ref, err)
	}

	genNum := 0
	if len(parts) >= 2 {
		genNum, err = strconv.Atoi(parts[1])
		if err != nil {
			genNum = 0
		}
	}

	return objNum, genNum, nil
}

// --------------------------------------------------------------------------
// Stream bounds
// --------------------------------------------------------------------------

// findStreamStart locates the "stream\r\n" or "stream\n" marker and returns
// the byte position right after it (where stream data begins). Returns -1 if
// not found.
func findStreamStart(data []byte, afterDictEnd int) int {
	// Search from afterDictEnd forward.
	search := data[afterDictEnd:]
	// Skip whitespace.
	idx := 0
	for idx < len(search) && isSpace(search[idx]) {
		idx++
	}
	// Look for "stream".
	streamIdx := bytes.Index(search[idx:], []byte("stream"))
	if streamIdx < 0 {
		return -1
	}
	streamIdx += idx

	// Position after "stream".
	pos := afterDictEnd + streamIdx + 6

	// Skip CRLF or LF after "stream".
	if pos < len(data) {
		if data[pos] == '\r' && pos+1 < len(data) && data[pos+1] == '\n' {
			pos += 2
		} else if data[pos] == '\n' {
			pos++
		}
	}

	return pos
}

// findStreamEnd locates "endstream" and returns the byte position before it
// (the end of stream data, exclusive). Returns -1 if not found.
func findStreamEnd(data []byte, streamStart int) int {
	search := data[streamStart:]
	endIdx := bytes.Index(search, []byte("endstream"))
	if endIdx < 0 {
		return -1
	}
	// Trim trailing whitespace before endstream.
	end := endIdx
	for end > 0 && (search[end-1] == '\r' || search[end-1] == '\n' || search[end-1] == ' ') {
		end--
	}
	return streamStart + end
}

// --------------------------------------------------------------------------
// Content stream text extraction
// --------------------------------------------------------------------------

// extractTextFromStream parses a PDF content stream byte slice and extracts
// text from Tj, TJ, ', " operators.
func extractTextFromStream(stream []byte) string {
	var result strings.Builder

	i := 0
	for i < len(stream) {
		// Skip whitespace.
		for i < len(stream) && isSpace(stream[i]) {
			i++
		}
		if i >= len(stream) {
			break
		}

		// Check for a string literal: ( ... )
		if stream[i] == '(' {
			str, end := readPDFString(stream, i)
			// The string may be followed by an operator (Tj, ', ", etc.).
			rawStr := str[1 : len(str)-1] // Strip parens.
			decoded := decodePDFString(rawStr)

			// Peek ahead for the operator.
			opStart := end
			for opStart < len(stream) && isSpace(stream[opStart]) {
				opStart++
			}

			// Read operator token.
			opEnd := opStart
			for opEnd < len(stream) && !isSpace(stream[opEnd]) {
				opEnd++
			}
			op := string(stream[opStart:opEnd])

			switch op {
			case "Tj", "'", "\"":
				if result.Len() > 0 {
					result.WriteString(" ")
				}
				result.WriteString(decoded)
				i = opEnd
				continue
			}

			// If the string is not followed by a recognized operator,
			// just save it if it's part of content.
			i = end
			continue
		}

		// Check for TJ operator: [( ... ) num ( ... ) num ... ]
		if stream[i] == '[' {
			arrayEnd := findMatchingBracket(stream, i)
			if arrayEnd < 0 {
				i++
				continue
			}
			arrayContent := stream[i : arrayEnd+1]
			// Check if followed by "TJ".
			afterEnd := arrayEnd + 1
			for afterEnd < len(stream) && isSpace(stream[afterEnd]) {
				afterEnd++
			}
			opEnd := afterEnd
			for opEnd < len(stream) && !isSpace(stream[opEnd]) {
				opEnd++
			}
			op := string(stream[afterEnd:opEnd])

			if op == "TJ" {
				// Extract strings from the array.
				texts := extractStringsFromTJArray(arrayContent)
				if len(texts) > 0 {
					if result.Len() > 0 {
						result.WriteString(" ")
					}
					result.WriteString(strings.Join(texts, " "))
				}
				i = opEnd
				continue
			}

			i = arrayEnd + 1
			continue
		}

		i++
	}

	return strings.TrimSpace(result.String())
}

// findMatchingBracket finds the position of the ']' matching the '[' at start.
func findMatchingBracket(data []byte, start int) int {
	if start >= len(data) || data[start] != '[' {
		return -1
	}
	depth := 1
	i := start + 1
	for i < len(data) && depth > 0 {
		switch data[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '(':
			// Skip string literal.
			_, i = readPDFString(data, i)
			if i >= len(data) {
				return -1
			}
			continue
		case '\\':
			i++ // skip escaped char
		}
		i++
	}
	if depth != 0 {
		return -1
	}
	return i - 1
}

// extractStringsFromTJArray extracts string literals from a TJ array content.
func extractStringsFromTJArray(array []byte) []string {
	var texts []string
	i := 0
	for i < len(array) {
		if array[i] == '(' {
			str, end := readPDFString(array, i)
			rawStr := str[1 : len(str)-1]
			decoded := decodePDFString(rawStr)
			if decoded != "" {
				texts = append(texts, decoded)
			}
			i = end
		} else {
			i++
		}
	}
	return texts
}

// decodePDFString handles PDF escape sequences within a string literal body
// (without the enclosing parentheses). Converts \(, \), \\, \n, \r, \t, \ddd
// (octal) to their actual characters.
func decodePDFString(s string) string {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				buf.WriteByte('\n')
				i += 2
			case 'r':
				buf.WriteByte('\r')
				i += 2
			case 't':
				buf.WriteByte('\t')
				i += 2
			case '\\':
				buf.WriteByte('\\')
				i += 2
			case '(':
				buf.WriteByte('(')
				i += 2
			case ')':
				buf.WriteByte(')')
				i += 2
			case '\r':
				// Line continuation: skip \r and optional \n.
				if i+2 < len(s) && s[i+2] == '\n' {
					i += 3
				} else {
					i += 2
				}
			case '\n':
				// Line continuation.
				i += 2
			default:
				// Octal: \ddd (1-3 octal digits).
				if s[i+1] >= '0' && s[i+1] <= '9' {
					octalEnd := i + 2
					for octalEnd < len(s) && octalEnd-i-1 < 3 && s[octalEnd] >= '0' && s[octalEnd] <= '7' {
						octalEnd++
					}
					octalVal, _ := strconv.ParseUint(s[i+1:octalEnd], 8, 8)
					buf.WriteByte(byte(octalVal))
					i = octalEnd
				} else {
					// Unknown escape, output the character as-is.
					i += 2
				}
			}
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

// --------------------------------------------------------------------------
// Utility helpers
// --------------------------------------------------------------------------

// decompressFlate decompresses data using zlib (FlateDecode).
func decompressFlate(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("zlib.NewReader: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("decompressing flate stream: %w", err)
	}
	return out, nil
}

// trimLeftSpace skips ASCII whitespace at the start of data and returns the
// remaining slice (non-destructive).
func trimLeftSpace(data []byte) []byte {
	i := 0
	for i < len(data) && isSpace(data[i]) {
		i++
	}
	return data[i:]
}

// isSpace reports whether b is a PDF whitespace character: space, tab, CR, LF,
// or form feed.
func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f'
}

// nextLine reads the next line from data starting at pos (updating pos to
// point past the newline). Returns the line content (excluding newline).
func nextLine(data []byte, pos *int) []byte {
	if *pos >= len(data) {
		return nil
	}
	start := *pos
	i := start
	for i < len(data) && data[i] != '\n' && data[i] != '\r' {
		i++
	}
	*pos = i
	// Consume the newline.
	if *pos < len(data) {
		if data[*pos] == '\r' {
			(*pos)++
		}
		if *pos < len(data) && data[*pos] == '\n' {
			(*pos)++
		}
	}
	return data[start:i]
}

// advanceLine advances the position past one line (used when we already read
// the content and just want to skip the line).
func advanceLine(data []byte, pos *int) {
	if *pos >= len(data) {
		return
	}
	for *pos < len(data) && data[*pos] != '\n' && data[*pos] != '\r' {
		(*pos)++
	}
	if *pos < len(data) {
		if data[*pos] == '\r' {
			(*pos)++
		}
		if *pos < len(data) && data[*pos] == '\n' {
			(*pos)++
		}
	}
}

// skipWhitespace advances pos past any whitespace characters.
func skipWhitespace(data []byte, pos *int) {
	for *pos < len(data) && isSpace(data[*pos]) {
		(*pos)++
	}
}

// parsePDFArray parses a PDF array string "[elem1 elem2 ...]" into individual
// element strings. Elements may be references ("1 0 R"), names ("/Type"),
// strings ("(hello)"), numbers, or nested arrays.
func parsePDFArray(s string) []string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' {
		return nil
	}
	// Strip outer brackets.
	s = s[1 : len(s)-1]

	var result []string
	i := 0
	for i < len(s) {
		// Skip whitespace.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
			i++
		}
		if i >= len(s) {
			break
		}

		switch s[i] {
		case '(':
			// String literal -- find matching ')'.
			depth := 1
			j := i + 1
			for j < len(s) && depth > 0 {
				if s[j] == '\\' {
					j += 2
					continue
				}
				if s[j] == '(' {
					depth++
				} else if s[j] == ')' {
					depth--
				}
				if depth > 0 {
					j++
				}
			}
			result = append(result, s[i:j+1])
			i = j + 1
		case '[':
			// Nested array.
			depth := 1
			j := i + 1
			for j < len(s) && depth > 0 {
				if s[j] == '[' {
					depth++
				} else if s[j] == ']' {
					depth--
				}
				j++
			}
			result = append(result, s[i:j])
			i = j
		case '<':
			if i+1 < len(s) && s[i+1] == '<' {
				// Nested dictionary.
				depth := 1
				j := i + 2
				for j < len(s)-1 && depth > 0 {
					if s[j] == '<' && s[j+1] == '<' {
						depth++
						j++
					} else if s[j] == '>' && s[j+1] == '>' {
						depth--
						j++
					}
					j++
				}
				result = append(result, s[i:j])
				i = j
			} else {
				// Hex string.
				j := i + 1
				for j < len(s) && s[j] != '>' {
					j++
				}
				result = append(result, s[i:j+1])
				i = j + 1
			}
		default:
			// Read until whitespace, ']', '(', '[', '<'.
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\r' && s[j] != '\n' &&
				s[j] != ']' && s[j] != '(' && s[j] != '[' && s[j] != '<' {
				j++
			}
			tok := s[i:j]
			// If the token looks like a number followed by another number and "R",
			// combine them into a reference.
			if isNumeric(tok) {
				// Peek ahead for "N R" pattern.
				peek := j
				for peek < len(s) && (s[peek] == ' ' || s[peek] == '\t') {
					peek++
				}
				peekStart := peek
				for peek < len(s) && s[peek] != ' ' && s[peek] != '\t' && s[peek] != ']' {
					peek++
				}
				tok2 := s[peekStart:peek]
				if isNumeric(tok2) {
					// Check for following "R".
					peek2 := peek
					for peek2 < len(s) && (s[peek2] == ' ' || s[peek2] == '\t') {
						peek2++
					}
					peekStart2 := peek2
					for peek2 < len(s) && s[peek2] != ' ' && s[peek2] != '\t' && s[peek2] != ']' {
						peek2++
					}
					tok3 := s[peekStart2:peek2]
					if tok3 == "R" {
						result = append(result, tok+" "+tok2+" R")
						i = peek2
						continue
					}
				}
				result = append(result, tok)
				i = j
			} else {
				result = append(result, tok)
				i = j
			}
		}
	}

	return result
}

// isNumeric reports whether s is a numeric string (optionally with leading
// sign and decimal point).
func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, c := range s {
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '-' && i == 0 {
			continue
		}
		if c == '.' && i > 0 {
			continue
		}
		return false
	}
	return true
}
