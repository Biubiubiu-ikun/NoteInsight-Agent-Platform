import type { Note } from "../types/api";

export const categories = [
  { value: "", label: "全部" },
  { value: "beauty", label: "美妆" },
  { value: "fashion", label: "穿搭" },
  { value: "food", label: "美食" },
  { value: "travel", label: "旅行" },
  { value: "home", label: "家居" },
  { value: "fitness", label: "运动" },
  { value: "career", label: "职场" },
  { value: "digital", label: "数码" },
  { value: "study", label: "学习" },
  { value: "local_life", label: "本地" },
] as const;

export function categoryLabel(category: string): string {
  return categories.find((item) => item.value === category)?.label ?? (category || "未分类");
}

export function formatCount(value: number): string {
  if (value >= 10_000) return `${(value / 10_000).toFixed(value >= 100_000 ? 0 : 1)}万`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}k`;
  return String(value);
}

export function formatDate(value: string): string {
  const date = new Date(value);
  return new Intl.DateTimeFormat("zh-CN", { month: "short", day: "numeric" }).format(date);
}

export function noteExcerpt(note: Note): string {
  return note.body.replace(/【[^】]+】/g, " ").replace(/\s+/g, " ").trim();
}

const spriteIndex: Record<string, number> = {
  local_life: 0,
  travel: 0,
  career: 0,
  study: 1,
  digital: 2,
  beauty: 3,
  fashion: 3,
  home: 4,
  food: 4,
  fitness: 5,
};

export function coverPosition(category: string): string {
  const index = spriteIndex[category] ?? 4;
  const positions = ["0% 0%", "50% 0%", "100% 0%", "0% 100%", "50% 100%", "100% 100%"];
  return positions[index];
}
