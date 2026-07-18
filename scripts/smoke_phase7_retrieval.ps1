param(
    [string]$BaseUrl = "http://127.0.0.1:18080",
    [long]$ProjectId = 1,
    [long]$DatasetVersionId = 2,
    [string]$IngestionRunId = "phase7a_dv2_rebuild_v2_20260718",
    [string[]]$Modes = @("lexical"),
    [string]$Query = ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String("5LiA5Lu96K6w5b2V5oqK5qC46aqM5oyH5qCH5YaZ5oiQ4oCc6K6w5b2V5YWo5aSp5q2l6KGM5ZCO55qE6IiS6YCC5bqm5ZKM6KGj54mp56e75L2N5qyh5pWw77yb6KeC5a+f5ZGo5pyfMjnlpKnvvIzlhbHlrozmiJAxMuasoeiusOW9le+8jOebuOWFs+mihOeul+e6pjI2MOWFg+KAneOAguWcqOaXqeaZmua4qeW3ruWNgeW6puS4lOmcgOimgeatpeihjOmAmuWLpOeahOadoeS7tuS4i++8jOmdouWQkeS4i+WNiui6q+mHj+aEn+aYjuaYvuOAgeaAleWPoOepv+aYvuiHg+iCv+eahOS6uueahOS6uu+8jOacgOe7iOW6lOS/neeVmeS7gOS5iOWIpOaWreWSjOmZkOWItu+8nw=="))),
    [ValidateSet("candidates", "no_relevant_document", "either")]
    [string]$ExpectedDecision = "candidates",
    [string]$AccessToken = ""
)

$ErrorActionPreference = "Stop"
$headers = @{}
if ($AccessToken) {
    $headers.Authorization = "Bearer $AccessToken"
}

foreach ($mode in $Modes) {
    $payload = @{
        project_id = $ProjectId
        dataset_version_id = $DatasetVersionId
        ingestion_run_id = $IngestionRunId
        query = $Query
        mode = $mode
        limit = 5
    } | ConvertTo-Json -Depth 5

    $response = Invoke-RestMethod `
        -Method Post `
        -Uri "$BaseUrl/api/v1/retrieval/search" `
        -Headers $headers `
        -ContentType "application/json; charset=utf-8" `
        -Body ([System.Text.Encoding]::UTF8.GetBytes($payload))

    if ($response.mode -ne $mode) {
        throw "retrieval mode mismatch: got $($response.mode), want $mode"
    }
    if ($response.scope.dataset_version_id -ne $DatasetVersionId -or
        $response.scope.ingestion_run_id -ne $IngestionRunId) {
        throw "retrieval scope does not match the requested frozen snapshot"
    }
    if ($ExpectedDecision -ne "either" -and $response.decision.status -ne $ExpectedDecision) {
        throw "retrieval decision mismatch for ${mode}: got $($response.decision.status), want $ExpectedDecision"
    }
    $expectedEmbeddingCalls = if ($mode -eq "lexical") { 0 } else { 1 }
    if ($response.embedding_calls -ne $expectedEmbeddingCalls -or
        $response.external_model_calls -ne $expectedEmbeddingCalls) {
        throw "retrieval model-call accounting mismatch for ${mode}"
    }
    if ($response.decision.status -eq "candidates") {
        if ($response.results.Count -eq 0) {
            throw "candidate decision returned no results"
        }
        foreach ($result in $response.results) {
            if ($result.citations.Count -eq 0) {
                throw "result chunk $($result.chunk_id) has no citation"
            }
            foreach ($citation in $result.citations) {
                if (-not $citation.citation_key -or -not $citation.quote -or
                    -not $citation.quote_hash -or $citation.source_end_byte -le $citation.source_start_byte) {
                    throw "result chunk $($result.chunk_id) has an incomplete citation"
                }
            }
        }
    }

    [pscustomobject]@{
        mode = $response.mode
        decision = $response.decision.status
        results = $response.results.Count
        candidates = $response.candidate_count
        took_ms = $response.took_ms
        embedding_calls = $response.embedding_calls
        retriever_version = $response.retriever_version
    }
}
