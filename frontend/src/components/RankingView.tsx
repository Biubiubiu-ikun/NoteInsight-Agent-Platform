import { Flame, LoaderCircle, RefreshCcw } from "lucide-react";
import { useEffect, useState } from "react";
import { apiFetch } from "../api/client";
import type { HotNoteItem, Note, Page } from "../types/api";
import { NoteCard } from "./NoteCard";

export function RankingView({ category, onOpen }: { category: string; onOpen: (noteId: number) => void }) {
  const [notes, setNotes] = useState<Note[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [retryKey, setRetryKey] = useState(0);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    const params = new URLSearchParams({ limit: "18" });
    if (category) params.set("category", category);
    apiFetch<Page<HotNoteItem>>(`/api/v1/rankings/notes/daily?${params}`)
      .then((page) => page.items.flatMap((item) => item.note ? [item.note] : []))
      .then((items) => { if (active) setNotes(items); })
      .catch(() => { if (active) setError("热榜暂时不可用，请确认 Redis 热榜数据已预热"); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [category, retryKey]);

  if (loading) return <div className="center-state"><LoaderCircle className="spin" /><span>正在计算今日热度</span></div>;
  if (error) return <div className="center-state"><Flame /><p>{error}</p><button className="secondary-button" onClick={() => setRetryKey((key) => key + 1)}><RefreshCcw size={16} />重试</button></div>;

  return (
    <div className="note-grid ranking-grid">
      {notes.map((note, index) => <NoteCard key={note.id} note={note} onOpen={onOpen} rank={index + 1} />)}
    </div>
  );
}
