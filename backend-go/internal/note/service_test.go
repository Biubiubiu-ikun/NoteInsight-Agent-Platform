package note

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockingReadRepository struct {
	noteRepository

	note         Note
	comments     []NoteComment
	commentsMore bool
	release      chan struct{}
	noteStarted  chan struct{}
	pageStarted  chan struct{}
	noteOnce     sync.Once
	pageOnce     sync.Once
	noteCalls    atomic.Int32
	pageCalls    atomic.Int32
	pageLimit    atomic.Int32
}

func (r *blockingReadRepository) CanReadNote(context.Context, int64, int64) (bool, error) {
	return true, nil
}

func (r *blockingReadRepository) GetNote(ctx context.Context, _ int64) (Note, error) {
	r.noteCalls.Add(1)
	r.noteOnce.Do(func() { close(r.noteStarted) })
	select {
	case <-ctx.Done():
		return Note{}, ctx.Err()
	case <-r.release:
		return r.note, nil
	}
}

func (r *blockingReadRepository) ListComments(
	ctx context.Context,
	_ int64,
	input ListCommentsInput,
	_ keysetCursor,
) ([]NoteComment, bool, error) {
	r.pageCalls.Add(1)
	r.pageLimit.Store(int32(input.Limit))
	r.pageOnce.Do(func() { close(r.pageStarted) })
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-r.release:
		return r.comments, r.commentsMore, nil
	}
}

func newBlockingReadRepository() *blockingReadRepository {
	return &blockingReadRepository{
		release:     make(chan struct{}),
		noteStarted: make(chan struct{}),
		pageStarted: make(chan struct{}),
	}
}

func TestServiceCreateNoteValidation(t *testing.T) {
	service := NewService(nil)

	_, err := service.CreateNote(context.Background(), CreateNoteInput{
		AuthorID: 10001,
		Title:    "",
		Body:     "body",
		Category: "beauty",
	})
	if err == nil {
		t.Fatal("CreateNote() expected validation error")
	}
}

func TestServiceParseIDValidation(t *testing.T) {
	service := NewService(nil)

	if _, err := service.GetNote(context.Background(), "not-an-id"); err == nil {
		t.Fatal("GetNote() expected validation error")
	}
}

func TestServiceListNotesDefaultsLimit(t *testing.T) {
	cursor, err := decodeNoteCursor("")
	if err != nil {
		t.Fatalf("decodeNoteCursor() error = %v", err)
	}
	if cursor.ID != 0 {
		t.Fatalf("empty cursor id = %d, want 0", cursor.ID)
	}
}

func TestServiceGetNoteCoalescesConcurrentCacheMisses(t *testing.T) {
	repo := newBlockingReadRepository()
	repo.note = Note{ID: 42, Title: "shared note"}
	service := NewService(repo)

	const callers = 20
	start := make(chan struct{})
	errorsCh := make(chan error, callers)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	for range callers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			note, err := service.GetNote(context.Background(), "42")
			if err == nil && note.ID != 42 {
				err = errors.New("unexpected note returned")
			}
			errorsCh <- err
		}()
	}

	ready.Wait()
	close(start)
	waitForSignal(t, repo.noteStarted)
	time.Sleep(50 * time.Millisecond)
	close(repo.release)
	done.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("GetNote() error = %v", err)
		}
	}
	if calls := repo.noteCalls.Load(); calls != 1 {
		t.Fatalf("repository GetNote() calls = %d, want 1", calls)
	}
}

func TestServiceListCommentsCoalescesFirstPageAndPreservesLimit(t *testing.T) {
	repo := newBlockingReadRepository()
	repo.commentsMore = true
	for id := int64(100); id > 0; id-- {
		repo.comments = append(repo.comments, NoteComment{
			ID:        id,
			NoteID:    7,
			CreatedAt: time.Unix(id, 0).UTC(),
		})
	}
	service := NewService(repo)

	const callers = 20
	start := make(chan struct{})
	errorsCh := make(chan error, callers)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	for range callers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			page, err := service.ListComments(context.Background(), ListCommentsInput{
				NoteID: "7",
				Limit:  20,
			})
			if err == nil && (len(page.Items) != 20 || page.NextCursor == "") {
				err = errors.New("first page did not preserve the requested limit and cursor")
			}
			errorsCh <- err
		}()
	}

	ready.Wait()
	close(start)
	waitForSignal(t, repo.pageStarted)
	time.Sleep(50 * time.Millisecond)
	close(repo.release)
	done.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("ListComments() error = %v", err)
		}
	}
	if calls := repo.pageCalls.Load(); calls != 1 {
		t.Fatalf("repository ListComments() calls = %d, want 1", calls)
	}
	if limit := repo.pageLimit.Load(); limit != maxCommentLimit {
		t.Fatalf("repository ListComments() limit = %d, want %d", limit, maxCommentLimit)
	}
}

func TestServiceGetNoteCallerCancellationDoesNotCancelSharedLoad(t *testing.T) {
	repo := newBlockingReadRepository()
	repo.note = Note{ID: 42, Title: "shared note"}
	service := NewService(repo)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := service.GetNote(leaderCtx, "42")
		leaderErr <- err
	}()
	waitForSignal(t, repo.noteStarted)

	followerResult := make(chan error, 1)
	go func() {
		note, err := service.GetNote(context.Background(), "42")
		if err == nil && note.ID != 42 {
			err = errors.New("unexpected note returned")
		}
		followerResult <- err
	}()
	time.Sleep(25 * time.Millisecond)
	cancelLeader()

	select {
	case err := <-leaderErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader GetNote() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("leader request did not return after cancellation")
	}

	close(repo.release)
	select {
	case err := <-followerResult:
		if err != nil {
			t.Fatalf("follower GetNote() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("follower request did not receive shared result")
	}
	if calls := repo.noteCalls.Load(); calls != 1 {
		t.Fatalf("repository GetNote() calls = %d, want 1", calls)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for repository call")
	}
}
