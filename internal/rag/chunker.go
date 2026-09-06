package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

type Chunker struct {
	MaxTokens     int
	OverlapTokens int
}

func (c Chunker) Chunk(document Document) []Chunk {
	maxTokens := c.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 200
	}
	overlap := c.OverlapTokens
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxTokens {
		overlap = maxTokens / 4
	}

	tokens := strings.Fields(document.Text)
	if len(tokens) == 0 {
		return nil
	}

	step := maxTokens - overlap
	if step <= 0 {
		step = maxTokens
	}

	var chunks []Chunk
	for start, index := 0, 0; start < len(tokens); start, index = start+step, index+1 {
		end := start + maxTokens
		if end > len(tokens) {
			end = len(tokens)
		}
		text := strings.Join(tokens[start:end], " ")
		chunks = append(chunks, Chunk{
			ID:          document.ID + "_chunk_" + hashPrefix(text, 12),
			DocumentID:  document.ID,
			ChunkIndex:  index,
			Text:        text,
			TokenCount:  len(tokens[start:end]),
			ContentHash: HashText(text),
			Metadata:    cloneMap(document.Metadata),
		})
		if end == len(tokens) {
			break
		}
	}
	return chunks
}

func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func hashPrefix(text string, n int) string {
	hash := HashText(text)
	if n > len(hash) {
		return hash
	}
	return hash[:n]
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return map[string]string{}
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
