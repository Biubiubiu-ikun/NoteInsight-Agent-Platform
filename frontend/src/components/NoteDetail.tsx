import {
  Bookmark,
  ChevronDown,
  Heart,
  LoaderCircle,
  MessageCircle,
  Pencil,
  Send,
  Share2,
  Trash2,
  X,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { ApiError, apiFetch } from "../api/client";
import { useAuth } from "../auth/AuthContext";
import coverSprite from "../assets/category-sprite.png";
import { categoryLabel, coverPosition, formatCount, formatDate } from "../lib/display";
import type { ActionResult, Note, NoteComment, Page, ShareResult } from "../types/api";

interface NoteDetailProps {
  noteId: number | null;
  onClose: () => void;
  onNeedAuth: () => void;
  onToast: (message: string) => void;
  onChanged: () => void;
}

type ActionName = "like" | "collect" | "share";

export function NoteDetail({ noteId, onClose, onNeedAuth, onToast, onChanged }: NoteDetailProps) {
  const { user } = useAuth();
  const [note, setNote] = useState<Note | null>(null);
  const [comments, setComments] = useState<NoteComment[]>([]);
  const [commentCursor, setCommentCursor] = useState("");
  const [commentText, setCommentText] = useState("");
  const [loading, setLoading] = useState(false);
  const [busyAction, setBusyAction] = useState<ActionName | "comment" | "delete" | "edit" | "">("");
  const [editing, setEditing] = useState(false);
  const [editTitle, setEditTitle] = useState("");
  const [editBody, setEditBody] = useState("");
  const [error, setError] = useState("");

  const loadComments = useCallback(async (id: number, cursor = "", append = false) => {
    const params = new URLSearchParams({ limit: "20" });
    if (cursor) params.set("cursor", cursor);
    const page = await apiFetch<Page<NoteComment>>(`/api/v1/notes/${id}/comments?${params}`);
    setComments((current) => append ? [...current, ...page.items] : page.items);
    setCommentCursor(page.next_cursor || "");
  }, []);

  useEffect(() => {
    if (!noteId) return;
    let active = true;
    setLoading(true);
    setError("");
    setEditing(false);
    Promise.all([apiFetch<Note>(`/api/v1/notes/${noteId}`), loadComments(noteId)])
      .then(([loaded]) => {
        if (!active) return;
        setNote(loaded);
        setEditTitle(loaded.title);
        setEditBody(loaded.body);
      })
      .catch((caught) => {
        if (active) setError(caught instanceof ApiError ? caught.message : "详情加载失败");
      })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [noteId, loadComments]);

  if (!noteId) return null;

  function requireUser(): boolean {
    if (user) return true;
    onNeedAuth();
    return false;
  }

  async function runAction(action: ActionName) {
    if (!note || !requireUser()) return;
    setBusyAction(action);
    try {
      if (action === "share") {
        const result = await apiFetch<ShareResult>(`/api/v1/notes/${note.id}/share`, {
          method: "POST",
          body: JSON.stringify({ channel: "web_console" }),
        });
        setNote({ ...note, share_count: result.count_pending ? note.share_count + 1 : result.share_count });
        await navigator.clipboard?.writeText(window.location.href);
        onToast("分享事件已记录，链接已复制");
      } else {
        const active = action === "like" ? note.viewer_liked : note.viewer_collected;
        const result = await apiFetch<ActionResult>(`/api/v1/notes/${note.id}/${action}`, {
          method: active ? "DELETE" : "POST",
          body: !active && action === "collect" ? JSON.stringify({ collection_name: "默认收藏" }) : undefined,
        });
        const field = action === "like" ? "like_count" : "collect_count";
        const stateField = action === "like" ? "viewer_liked" : "viewer_collected";
        const delta = active ? -1 : 1;
        const nextCount = result.count_pending && result.applied ? Math.max(0, note[field] + delta) : result.count;
        setNote({ ...note, [field]: nextCount, [stateField]: result.applied ? !active : active });
        onToast(result.applied ? (active ? "已取消" : action === "like" ? "已点赞" : "已收藏") : "状态没有变化");
      }
      onChanged();
    } catch (caught) {
      onToast(caught instanceof ApiError ? caught.message : "操作失败");
    } finally {
      setBusyAction("");
    }
  }

  async function createComment(event: React.FormEvent) {
    event.preventDefault();
    if (!note || !commentText.trim() || !requireUser()) return;
    setBusyAction("comment");
    try {
      const created = await apiFetch<NoteComment>(`/api/v1/notes/${note.id}/comments`, {
        method: "POST",
        body: JSON.stringify({ content: commentText.trim(), intent: "discussion" }),
      });
      setComments((current) => [created, ...current]);
      setNote({ ...note, comment_count: note.comment_count + 1 });
      setCommentText("");
      onToast("评论已发布");
      onChanged();
    } catch (caught) {
      onToast(caught instanceof ApiError ? caught.message : "评论失败");
    } finally {
      setBusyAction("");
    }
  }

  async function likeComment(comment: NoteComment) {
    if (!requireUser()) return;
    try {
      const result = await apiFetch<ActionResult>(`/api/v1/comments/${comment.id}/like`, { method: "POST" });
      setComments((current) => current.map((item) => item.id === comment.id ? {
        ...item,
        like_count: result.count_pending && result.applied ? item.like_count + 1 : result.count,
      } : item));
      onToast(result.applied ? "已点赞评论" : "这条评论已经点过赞");
    } catch (caught) {
      onToast(caught instanceof ApiError ? caught.message : "操作失败");
    }
  }

  async function deleteComment(comment: NoteComment) {
    if (!user || (comment.user_id !== user.id && user.role !== "admin")) return;
    try {
      await apiFetch<{ status: string }>(`/api/v1/comments/${comment.id}`, { method: "DELETE" });
      setComments((current) => current.filter((item) => item.id !== comment.id));
      onToast("评论已删除");
    } catch (caught) {
      onToast(caught instanceof ApiError ? caught.message : "删除失败");
    }
  }

  async function saveEdit(event: React.FormEvent) {
    event.preventDefault();
    if (!note) return;
    setBusyAction("edit");
    try {
      const updated = await apiFetch<Note>(`/api/v1/notes/${note.id}`, {
        method: "PATCH",
        body: JSON.stringify({ title: editTitle.trim(), body: editBody.trim() }),
      });
      setNote(updated);
      setEditing(false);
      onChanged();
      onToast("笔记已更新");
    } catch (caught) {
      onToast(caught instanceof ApiError ? caught.message : "更新失败");
    } finally {
      setBusyAction("");
    }
  }

  async function deleteNote() {
    if (!note || !window.confirm("确认软删除这篇笔记？")) return;
    setBusyAction("delete");
    try {
      await apiFetch<{ status: string }>(`/api/v1/notes/${note.id}`, { method: "DELETE" });
      onChanged();
      onToast("笔记已软删除");
      onClose();
    } catch (caught) {
      onToast(caught instanceof ApiError ? caught.message : "删除失败");
    } finally {
      setBusyAction("");
    }
  }

  const canManage = Boolean(note && user && (user.id === note.author_id || user.role === "admin"));

  return (
    <div className="detail-backdrop" role="presentation" onMouseDown={onClose}>
      <aside className="detail-drawer" role="dialog" aria-modal="true" aria-label="笔记详情" onMouseDown={(event) => event.stopPropagation()}>
        <div className="detail-toolbar">
          <button className="icon-button" type="button" onClick={onClose} aria-label="关闭详情"><X size={20} /></button>
          <span>笔记 #{noteId}</span>
          <div className="toolbar-actions">
            {canManage && <button className="icon-button" type="button" onClick={() => setEditing((value) => !value)} aria-label="编辑笔记"><Pencil size={18} /></button>}
            {canManage && <button className="icon-button danger" type="button" disabled={busyAction === "delete"} onClick={deleteNote} aria-label="删除笔记"><Trash2 size={18} /></button>}
          </div>
        </div>

        {loading && <div className="center-state detail-state"><LoaderCircle className="spin" /><span>加载完整内容</span></div>}
        {error && <div className="center-state detail-state"><p>{error}</p></div>}
        {!loading && note && (
          <div className="detail-scroll">
            <div className="detail-cover" style={{ backgroundImage: `url(${coverSprite})`, backgroundPosition: coverPosition(note.category) }} role="img" aria-label={`${categoryLabel(note.category)}主题封面`}>
              <span>{categoryLabel(note.category)}</span>
              <strong>Q {note.quality_score.toFixed(2)}</strong>
            </div>

            {editing ? (
              <form className="edit-form" onSubmit={saveEdit}>
                <label><span>标题</span><input required value={editTitle} onChange={(e) => setEditTitle(e.target.value)} /></label>
                <label><span>正文</span><textarea required rows={14} value={editBody} onChange={(e) => setEditBody(e.target.value)} /></label>
                <div className="form-actions"><button className="secondary-button" type="button" onClick={() => setEditing(false)}>取消</button><button className="primary-button" disabled={busyAction === "edit"}>保存修改</button></div>
              </form>
            ) : (
              <article className="detail-article">
                <p className="detail-kicker">{note.author?.nickname || note.author?.username || `作者 ${note.author_id}`} · {formatDate(note.created_at)} · {note.location?.city || "未标注地点"}</p>
                <h1>{note.title}</h1>
                <div className="topic-row">{(note.tags || []).map((tag) => <span key={tag}>#{tag}</span>)}</div>
                <div className="article-body">{note.body.split("\n").map((line, index) => line ? <p key={index}>{line}</p> : <br key={index} />)}</div>
              </article>
            )}

            {note.media && note.media.length > 0 && (
              <section className="media-section" aria-labelledby="media-title">
                <div className="section-heading"><div><span>STRUCTURED MEDIA</span><h2 id="media-title">图文卡片文字资产</h2></div><small>{note.media.length} 张占位媒体</small></div>
                <div className="media-card-grid">
                  {note.media.map((media, index) => (
                    <article className={`media-text-card tone-${index % 4}`} key={media.id}>
                      <div><span>{String(media.position).padStart(2, "0")}</span><strong>{media.caption}</strong></div>
                      <p>{media.ocr_text}</p>
                    </article>
                  ))}
                </div>
              </section>
            )}

            <div className="detail-actions" aria-label="笔记互动">
              <button className={note.viewer_liked ? "active" : ""} type="button" disabled={busyAction === "like"} onClick={() => runAction("like")}><Heart size={18} />{formatCount(note.like_count)}</button>
              <button className={note.viewer_collected ? "active" : ""} type="button" disabled={busyAction === "collect"} onClick={() => runAction("collect")}><Bookmark size={18} />{formatCount(note.collect_count)}</button>
              <button type="button" disabled={busyAction === "share"} onClick={() => runAction("share")}><Share2 size={18} />{formatCount(note.share_count)}</button>
              <span><MessageCircle size={18} />{formatCount(note.comment_count)}</span>
            </div>

            <section className="comment-section" aria-labelledby="comments-title">
              <div className="section-heading"><div><span>DISCUSSION</span><h2 id="comments-title">评论</h2></div></div>
              <form className="comment-composer" onSubmit={createComment}>
                <textarea rows={3} value={commentText} onChange={(e) => setCommentText(e.target.value)} placeholder={user ? "写下你的验证结果或疑问" : "登录后参与讨论"} onFocus={() => { if (!user) onNeedAuth(); }} />
                <button className="primary-button" type="submit" disabled={!commentText.trim() || busyAction === "comment"}>{busyAction === "comment" ? <LoaderCircle className="spin" size={17} /> : <Send size={17} />}发布</button>
              </form>
              <div className="comment-list">
                {comments.map((comment) => (
                  <article className="comment-item" key={comment.id}>
                    <div className="avatar small-avatar">{String(comment.user_id).slice(-2)}</div>
                    <div className="comment-content">
                      <div><strong>用户 {comment.user_id}</strong><time>{formatDate(comment.created_at)}</time></div>
                      <p>{comment.content}</p>
                      <div className="comment-tools">
                        <button type="button" onClick={() => likeComment(comment)}><Heart size={14} />{comment.like_count}</button>
                        {user && (user.id === comment.user_id || user.role === "admin") && <button className="danger-text" type="button" onClick={() => deleteComment(comment)}>删除</button>}
                      </div>
                    </div>
                  </article>
                ))}
              </div>
              {commentCursor && <button className="text-button load-comments" type="button" onClick={() => loadComments(note.id, commentCursor, true)}><ChevronDown size={16} />加载更多评论</button>}
            </section>
          </div>
        )}
      </aside>
    </div>
  );
}
