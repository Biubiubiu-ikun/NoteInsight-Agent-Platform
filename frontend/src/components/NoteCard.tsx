import { Bookmark, Heart, MapPin, MessageCircle } from "lucide-react";
import coverSprite from "../assets/category-sprite.png";
import { categoryLabel, coverPosition, formatCount, formatDate, noteExcerpt } from "../lib/display";
import type { Note } from "../types/api";

interface NoteCardProps {
  note: Note;
  onOpen: (noteId: number) => void;
  rank?: number;
}

export function NoteCard({ note, onOpen, rank }: NoteCardProps) {
  return (
    <article
      className="note-card"
      role="button"
      tabIndex={0}
      onClick={() => onOpen(note.id)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") onOpen(note.id);
      }}
      aria-label={`打开笔记：${note.title}`}
    >
      <div
        className="note-cover"
        role="img"
        aria-label={`${categoryLabel(note.category)}主题素材封面`}
        style={{ backgroundImage: `url(${coverSprite})`, backgroundPosition: coverPosition(note.category) }}
      >
        {rank && <span className="rank-number">{String(rank).padStart(2, "0")}</span>}
        <span className="category-label">{categoryLabel(note.category)}</span>
        {note.location?.city && <span className="location-label"><MapPin size={13} />{note.location.city}</span>}
      </div>
      <div className="note-card-body">
        <h3>{note.title}</h3>
        <p>{noteExcerpt(note)}</p>
        <div className="note-card-meta">
          <span>{note.author?.nickname || note.author?.username || `作者 ${note.author_id}`}</span>
          <span>{formatDate(note.created_at)}</span>
        </div>
        <div className="note-card-stats" aria-label="互动数据">
          <span><Heart size={15} />{formatCount(note.like_count)}</span>
          <span><Bookmark size={15} />{formatCount(note.collect_count)}</span>
          <span><MessageCircle size={15} />{formatCount(note.comment_count)}</span>
          <strong>Q {note.quality_score.toFixed(2)}</strong>
        </div>
      </div>
    </article>
  );
}
