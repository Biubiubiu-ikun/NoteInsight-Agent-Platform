param(
    [string]$BaseUrl = "http://127.0.0.1:18080",
    [int]$ConvergenceTimeoutSeconds = 30
)

$ErrorActionPreference = "Stop"

function Invoke-Api {
    param(
        [string]$Method,
        [string]$Path,
        [object]$Body,
        [string]$Token
    )

    $headers = @{}
    if ($Token) {
        $headers.Authorization = "Bearer $Token"
    }
    $parameters = @{
        Method          = $Method
        Uri             = "$BaseUrl$Path"
        Headers         = $headers
        UseBasicParsing = $true
    }
    if ($null -ne $Body) {
        $parameters.ContentType = "application/json"
        $parameters.Body = $Body | ConvertTo-Json -Compress -Depth 10
    }

    try {
        $response = Invoke-WebRequest @parameters
        $content = if ($response.Content) { $response.Content | ConvertFrom-Json } else { $null }
        return [pscustomobject]@{ Status = [int]$response.StatusCode; Body = $content }
    }
    catch {
        $errorRecord = $_
        $errorResponse = $errorRecord.Exception.Response
        if ($null -eq $errorResponse) {
            throw
        }

        $status = [int]$errorResponse.StatusCode
        $raw = $null
        if ($null -ne $errorResponse.Content -and
            $errorResponse.Content.PSObject.Methods.Name -contains "ReadAsStringAsync") {
            $raw = $errorResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        }
        elseif ($errorResponse.PSObject.Methods.Name -contains "GetResponseStream") {
            $reader = [IO.StreamReader]::new($errorResponse.GetResponseStream())
            try {
                $raw = $reader.ReadToEnd()
            }
            finally {
                $reader.Dispose()
            }
        }
        elseif ($errorRecord.ErrorDetails.Message) {
            $raw = $errorRecord.ErrorDetails.Message
        }

        $content = if ($raw) { $raw | ConvertFrom-Json } else { $null }
        return [pscustomobject]@{ Status = $status; Body = $content }
    }
}

function Assert-Status([object]$Response, [int]$Expected, [string]$Label) {
    if ($Response.Status -ne $Expected) {
        throw "$Label returned $($Response.Status), expected $Expected. Body: $($Response.Body | ConvertTo-Json -Compress -Depth 10)"
    }
    Write-Host "PASS $Label ($Expected)"
}

function Invoke-Psql([string]$Sql) {
    $value = docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -Atc $Sql
    if ($LASTEXITCODE -ne 0) {
        throw "PostgreSQL assertion query failed."
    }
    return ($value | Out-String).Trim()
}

function Wait-ForPipeline {
    param([int64]$NoteId, [int64]$CommentId)

    $deadline = (Get-Date).AddSeconds($ConvergenceTimeoutSeconds)
    do {
        $state = Invoke-Psql "
SELECT
  n.like_count,
  (SELECT COUNT(*) FROM note_likes WHERE note_id = $NoteId),
  n.collect_count,
  (SELECT COUNT(*) FROM note_collects WHERE note_id = $NoteId),
  c.like_count,
  (SELECT COUNT(*) FROM note_comment_likes WHERE comment_id = $CommentId),
  (SELECT COUNT(*) FROM outbox_events WHERE status IN ('pending','processing','retry'))
FROM notes n
JOIN note_comments c ON c.id = $CommentId
WHERE n.id = $NoteId;"
        $parts = $state -split '\|'
        $ready = $parts.Count -eq 7 -and
            $parts[0] -eq $parts[1] -and
            $parts[2] -eq $parts[3] -and
            $parts[4] -eq $parts[5] -and
            $parts[6] -eq "0"
        if (-not $ready) {
            Start-Sleep -Milliseconds 500
        }
    } while (-not $ready -and (Get-Date) -lt $deadline)

    if (-not $ready) {
        throw "Async counters did not converge: $state"
    }
}

$ready = Invoke-Api -Method Get -Path "/ready"
Assert-Status $ready 200 "backend readiness"

$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$username1 = "phase2c_owner_$suffix"
$username2 = "phase2c_peer_$suffix"
$password = "Strong_password_123"

$register1 = Invoke-Api -Method Post -Path "/api/v1/auth/register" -Body @{
    username = $username1
    password = $password
    nickname = "Phase2C Owner"
}
Assert-Status $register1 201 "register owner"
$ownerId = [int64]$register1.Body.user.id
$ownerToken = [string]$register1.Body.access_token
$ownerRefresh = [string]$register1.Body.refresh_token

$duplicate = Invoke-Api -Method Post -Path "/api/v1/auth/register" -Body @{
    username = $username1
    password = $password
    nickname = "Duplicate"
}
Assert-Status $duplicate 409 "duplicate username"

$passwordHash = Invoke-Psql "SELECT password_hash FROM user_credentials WHERE user_id = $ownerId;"
if (-not $passwordHash -or $passwordHash -eq $password) {
    throw "Password hash assertion failed."
}
Write-Host "PASS password stored as hash"

$login = Invoke-Api -Method Post -Path "/api/v1/auth/login" -Body @{ username = $username1; password = $password }
Assert-Status $login 200 "login"
if (-not $login.Body.access_token -or -not $login.Body.refresh_token) {
    throw "Login did not return both tokens."
}

$wrongPassword = Invoke-Api -Method Post -Path "/api/v1/auth/login" -Body @{ username = $username1; password = "wrong_password" }
Assert-Status $wrongPassword 401 "wrong password"

$refresh = Invoke-Api -Method Post -Path "/api/v1/auth/refresh" -Body @{ refresh_token = $ownerRefresh }
Assert-Status $refresh 200 "refresh token"

$anonymousMe = Invoke-Api -Method Get -Path "/api/v1/me"
Assert-Status $anonymousMe 401 "anonymous me"
$me = Invoke-Api -Method Get -Path "/api/v1/me" -Token $ownerToken
Assert-Status $me 200 "authenticated me"
if ([int64]$me.Body.id -ne $ownerId) {
    throw "GET /me returned the wrong user."
}

$register2 = Invoke-Api -Method Post -Path "/api/v1/auth/register" -Body @{
    username = $username2
    password = $password
    nickname = "Phase2C Peer"
}
Assert-Status $register2 201 "register peer"
$peerId = [int64]$register2.Body.user.id
$peerToken = [string]$register2.Body.access_token

$noteBody = @{
    author_id = $peerId
    title = "Phase 2C automated acceptance $suffix"
    body = "Authentication, ownership, banned-user, and idempotency verification."
    category = "engineering"
    media = @(@{ media_type = "image"; caption = "auth smoke"; ocr_text = "phase2c acceptance"; position = 1 })
}
$anonymousCreate = Invoke-Api -Method Post -Path "/api/v1/notes" -Body $noteBody
Assert-Status $anonymousCreate 401 "anonymous create note"
$createNote = Invoke-Api -Method Post -Path "/api/v1/notes" -Body $noteBody -Token $ownerToken
Assert-Status $createNote 201 "authenticated create note"
$noteId = [int64]$createNote.Body.id
if ([int64]$createNote.Body.author_id -ne $ownerId) {
    throw "Request body author_id was trusted instead of the bearer token."
}
Write-Host "PASS note author comes from bearer token"

$peerEdit = Invoke-Api -Method Patch -Path "/api/v1/notes/$noteId" -Body @{ title = "forbidden edit" } -Token $peerToken
Assert-Status $peerEdit 403 "non-owner edit"
$ownerEdit = Invoke-Api -Method Patch -Path "/api/v1/notes/$noteId" -Body @{ title = "Phase 2C owner edit $suffix" } -Token $ownerToken
Assert-Status $ownerEdit 200 "owner edit"
$peerDelete = Invoke-Api -Method Delete -Path "/api/v1/notes/$noteId" -Token $peerToken
Assert-Status $peerDelete 403 "non-owner delete"

$commentBody = @{ user_id = $peerId; content = "Phase 2C authenticated comment"; intent = "test" }
$anonymousComment = Invoke-Api -Method Post -Path "/api/v1/notes/$noteId/comments" -Body $commentBody
Assert-Status $anonymousComment 401 "anonymous comment"
$createComment = Invoke-Api -Method Post -Path "/api/v1/notes/$noteId/comments" -Body $commentBody -Token $ownerToken
Assert-Status $createComment 201 "authenticated comment"
$commentId = [int64]$createComment.Body.id
if ([int64]$createComment.Body.user_id -ne $ownerId) {
    throw "Request body user_id was trusted instead of the bearer token."
}
Write-Host "PASS comment user comes from bearer token"

$like1 = Invoke-Api -Method Post -Path "/api/v1/notes/$noteId/like" -Body @{} -Token $ownerToken
$like2 = Invoke-Api -Method Post -Path "/api/v1/notes/$noteId/like" -Body @{} -Token $ownerToken
Assert-Status $like1 200 "first note like"
Assert-Status $like2 200 "duplicate note like"
if (-not $like1.Body.applied -or $like2.Body.applied) {
    throw "Note like idempotency response is incorrect."
}

$collect1 = Invoke-Api -Method Post -Path "/api/v1/notes/$noteId/collect" -Body @{ collection_name = "smoke" } -Token $ownerToken
$collect2 = Invoke-Api -Method Post -Path "/api/v1/notes/$noteId/collect" -Body @{ collection_name = "smoke" } -Token $ownerToken
Assert-Status $collect1 200 "first note collect"
Assert-Status $collect2 200 "duplicate note collect"
if (-not $collect1.Body.applied -or $collect2.Body.applied) {
    throw "Note collect idempotency response is incorrect."
}

$commentLike1 = Invoke-Api -Method Post -Path "/api/v1/comments/$commentId/like" -Body @{} -Token $ownerToken
$commentLike2 = Invoke-Api -Method Post -Path "/api/v1/comments/$commentId/like" -Body @{} -Token $ownerToken
Assert-Status $commentLike1 200 "first comment like"
Assert-Status $commentLike2 200 "duplicate comment like"
if (-not $commentLike1.Body.applied -or $commentLike2.Body.applied) {
    throw "Comment like idempotency response is incorrect."
}
Wait-ForPipeline -NoteId $noteId -CommentId $commentId
$factCounts = Invoke-Psql "SELECT (SELECT COUNT(*) FROM note_likes WHERE note_id=$noteId AND user_id=$ownerId),(SELECT COUNT(*) FROM note_collects WHERE note_id=$noteId AND user_id=$ownerId),(SELECT COUNT(*) FROM note_comment_likes WHERE comment_id=$commentId AND user_id=$ownerId);"
if ($factCounts -ne "1|1|1") {
    throw "Database uniqueness assertion failed: $factCounts"
}
Write-Host "PASS idempotent facts and async counters"

$secondComment = Invoke-Api -Method Post -Path "/api/v1/notes/$noteId/comments" -Body @{ content = "Pagination verification" } -Token $ownerToken
Assert-Status $secondComment 201 "second comment"
$firstPage = Invoke-Api -Method Get -Path "/api/v1/notes/$noteId/comments?limit=1"
Assert-Status $firstPage 200 "comment first page"
if (-not $firstPage.Body.next_cursor) {
    throw "Comment keyset pagination did not return next_cursor."
}
$cursor = [Uri]::EscapeDataString([string]$firstPage.Body.next_cursor)
$secondPage = Invoke-Api -Method Get -Path "/api/v1/notes/$noteId/comments?limit=1&cursor=$cursor"
Assert-Status $secondPage 200 "comment keyset next page"

Invoke-Psql "UPDATE users SET status='banned', updated_at=now() WHERE id=$peerId;" | Out-Null
foreach ($case in @(
    @{ Label = "banned create note"; Method = "Post"; Path = "/api/v1/notes"; Body = $noteBody },
    @{ Label = "banned comment"; Method = "Post"; Path = "/api/v1/notes/$noteId/comments"; Body = @{ content = "blocked" } },
    @{ Label = "banned like"; Method = "Post"; Path = "/api/v1/notes/$noteId/like"; Body = @{} },
    @{ Label = "banned collect"; Method = "Post"; Path = "/api/v1/notes/$noteId/collect"; Body = @{} }
)) {
    $response = Invoke-Api -Method $case.Method -Path $case.Path -Body $case.Body -Token $peerToken
    Assert-Status $response 403 $case.Label
}

Invoke-Psql "UPDATE users SET status='active', role='admin', updated_at=now() WHERE id=$peerId;" | Out-Null
$adminDelete = Invoke-Api -Method Delete -Path "/api/v1/notes/$noteId" -Token $peerToken
Assert-Status $adminDelete 200 "admin delete another user's note"

$logout = Invoke-Api -Method Post -Path "/api/v1/auth/logout" -Body @{ refresh_token = $ownerRefresh } -Token $ownerToken
Assert-Status $logout 200 "logout"
$refreshAfterLogout = Invoke-Api -Method Post -Path "/api/v1/auth/refresh" -Body @{ refresh_token = $ownerRefresh }
Assert-Status $refreshAfterLogout 401 "refresh after logout"

Write-Host "Phase 2C automated acceptance passed. owner_id=$ownerId peer_id=$peerId note_id=$noteId"
