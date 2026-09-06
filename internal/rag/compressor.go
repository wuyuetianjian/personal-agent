package rag

import "strings"

type ContextExcerpt struct {
	Text         string
	EvidenceID   string
	SourceURI    string
	Title        string
	Score        float64
	Metadata     map[string]string
	PrivacyClass string
}

type CompressedContext struct {
	Query           string
	Text            string
	Excerpts        []ContextExcerpt
	EvidenceIDs     []string
	OmittedEvidence []string
}

type Compressor struct {
	MaxChars int
}

func (c Compressor) Compress(query string, results []Result) CompressedContext {
	maxChars := c.MaxChars
	if maxChars <= 0 {
		maxChars = 4000
	}
	output := CompressedContext{Query: query}
	var builder strings.Builder
	for _, result := range results {
		evidenceID := result.EvidenceID
		if evidenceID == "" {
			evidenceID = result.Chunk.ID
		}
		excerptText := strings.TrimSpace(result.Chunk.Text)
		if excerptText == "" {
			continue
		}
		if builder.Len()+len(excerptText) > maxChars {
			output.OmittedEvidence = append(output.OmittedEvidence, evidenceID)
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString("[")
		builder.WriteString(evidenceID)
		builder.WriteString("] ")
		builder.WriteString(excerptText)
		output.EvidenceIDs = append(output.EvidenceIDs, evidenceID)
		output.Excerpts = append(output.Excerpts, ContextExcerpt{
			Text:         excerptText,
			EvidenceID:   evidenceID,
			SourceURI:    result.Document.SourceURI,
			Title:        result.Document.Title,
			Score:        result.Score,
			Metadata:     cloneMap(result.Chunk.Metadata),
			PrivacyClass: result.Document.PrivacyClass,
		})
	}
	output.Text = builder.String()
	return output
}
