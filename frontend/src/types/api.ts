export interface User {
  id: number;
  username: string;
  nickname: string;
  avatar_url: string;
  bio: string;
  role: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface AuthResponse {
  user: User;
  access_token: string;
  refresh_token?: string;
  expires_in: number;
}

export interface NoteMedia {
  id: number;
  note_id: number;
  media_type: string;
  url: string;
  caption: string;
  ocr_text: string;
  position: number;
  metadata: Record<string, unknown>;
  created_at: string;
}

export interface Note {
  id: number;
  project_id: number;
  author_id: number;
  title: string;
  body: string;
  category: string;
  topics: string[] | null;
  tags: string[] | null;
  location: { city?: string; synthetic?: boolean } | null;
  product_entities: Array<{ name?: string; type?: string }> | null;
  note_type: string;
  view_count: number;
  like_count: number;
  collect_count: number;
  comment_count: number;
  share_count: number;
  hot_score: number;
  quality_score: number;
  status: string;
  visibility: "public" | "project";
  content_version: number;
  viewer_liked: boolean;
  viewer_collected: boolean;
  author?: {
    id: number;
    username: string;
    nickname: string;
    avatar_url: string;
  };
  media?: NoteMedia[];
  created_at: string;
  updated_at: string;
}

export interface NoteComment {
  id: number;
  note_id: number;
  user_id: number;
  parent_id: number;
  root_id: number;
  content: string;
  like_count: number;
  reply_count: number;
  sentiment?: string;
  intent?: string;
  status: number;
  created_at: string;
  updated_at: string;
}

export interface Page<T> {
  items: T[];
  next_cursor?: string;
}

export interface HotNoteItem {
  note_id: number;
  score: number;
  note?: Note;
}

export interface ActionResult {
  resource_id: number;
  user_id: number;
  applied: boolean;
  count: number;
  count_pending: boolean;
  action: string;
}

export interface ShareResult {
  note_id: number;
  user_id: number;
  share_id: number;
  share_count: number;
  count_pending: boolean;
  channel: string;
}

export interface ApiErrorBody {
  error?: string;
  detail?: string;
}
