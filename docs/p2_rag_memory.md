# P2 RAG And Memory

Archived stage record: `docs/projdocs/task/P2.md`.

## Scope

P2 adds library-level local retrieval and memory capabilities. It does not change the CLI execution path, which remains a local no-op runner and interactive episodic memory shell.

## Requirements

- Add a RAG document and chunk model backed by existing `documents` and `document_chunks` tables.
- Add deterministic text chunking with stable content hashes and caller-controlled chunk sizing.
- Add local BM25 retrieval over indexed chunks.
- Add Reciprocal Rank Fusion (RRF) for combining ranked result lists with deterministic tie-breaking.
- Add a Qdrant HTTP client wrapper whose endpoint and collection are supplied by caller configuration.
- Add a reranker interface boundary compatible with later model-backed reranking.
- Add a context compressor that preserves evidence IDs and source metadata for every included excerpt.
- Add working, episodic, and semantic memory stores.
- Add a semantic memory indexing hook so durable facts can be indexed into RAG.

## Design

The `internal/rag` package owns document ingestion, chunking, retrieval, fusion, reranking contracts, Qdrant boundary code, and context compression. `SQLiteStore` persists documents and chunks through the existing P0 migration schema. `Chunker` is deterministic and hashes chunk text so retrieval results can be traced back to persisted rows.

BM25 runs locally over chunk text loaded from storage. Tokenization is intentionally small and dependency-free for P2: lowercase alphanumeric terms are indexed, term frequency is normalized with standard BM25 constants, and final ordering is stable by score, document ID, chunk index, and chunk ID.

The Qdrant wrapper is a narrow HTTP boundary that accepts its base URL, collection, API key, and HTTP client from the caller. Tests use an environment-controlled integration test and skip cleanly when Qdrant is unavailable.

The `internal/memory` package keeps the existing episodic store API for CLI compatibility and adds explicit working and semantic stores. Working memory supports task-scoped values with expiration cleanup. Semantic memory persists durable facts and optionally indexes each fact into a RAG indexer with evidence IDs retained in chunk metadata.

## Validation

- BM25 ranking must prefer chunks with stronger lexical matches.
- RRF must produce deterministic ordering for tied scores.
- Qdrant integration must skip cleanly when `QDRANT_URL` is unset or unavailable.
- Working, episodic, and semantic memory lifecycle tests must pass.
- Context compression must keep evidence IDs for every included result.
