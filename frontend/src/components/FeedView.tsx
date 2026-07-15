import { LoaderCircle, RefreshCcw, SearchX } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { ApiError, apiFetch } from "../api/client";
import type { Note, Page } from "../types/api";
import { NoteCard } from "./NoteCard";

interface FeedViewProps {
  category: string;
  query: string;
  onOpen: (noteId: number) => void;
  refreshKey: number;
}

export function FeedView({ category, query, onOpen, refreshKey }: FeedViewProps) {
  const [notes, setNotes] = useState<Note[]>([]);
  const [cursor, setCursor] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async (nextCursor = "", append = false) => {
    append ? setLoadingMore(true) : setLoading(true);
    setError("");
    try {
      const params = new URLSearchParams({ limit: "24" });
      if (category) params.set("category", category);
      if (query.trim()) params.set("q", query.trim());
      if (nextCursor) params.set("cursor", nextCursor);
      const page = await apiFetch<Page<Note>>(`/api/v1/notes?${params}`);
      setNotes((current) => (append ? [...current, ...page.items] : page.items));
      setCursor(page.next_cursor || "");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "笔记流加载失败");
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }, [category, query]);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 250);
    return () => window.clearTimeout(timer);
  }, [load, refreshKey]);

  if (loading) return <div className="center-state"><LoaderCircle className="spin" /><span>正在读取笔记流</span></div>;
  if (error) return <div className="center-state"><p>{error}</p><button className="secondary-button" onClick={() => load()}><RefreshCcw size={16} />重试</button></div>;
  if (!notes.length) return <div className="center-state"><SearchX /><p>当前筛选下没有匹配笔记</p></div>;

  return (
    <>
      <div className="note-grid">
        {notes.map((note) => <NoteCard key={note.id} note={note} onOpen={onOpen} />)}
      </div>
      {cursor && (
        <div className="load-more-row">
          <button className="secondary-button" type="button" disabled={loadingMore} onClick={() => load(cursor, true)}>
            {loadingMore && <LoaderCircle className="spin" size={16} />}加载更多
          </button>
        </div>
      )}
    </>
  );
}
