import {
  Activity,
  Bell,
  Compass,
  Flame,
  LogIn,
  Menu,
  PenLine,
  Search,
  Sparkles,
} from "lucide-react";
import { useEffect, useState } from "react";
import { AuthDialog } from "./auth/AuthDialog";
import { useAuth } from "./auth/AuthContext";
import { ComposeDialog } from "./components/ComposeDialog";
import { FeedView } from "./components/FeedView";
import { NoteDetail } from "./components/NoteDetail";
import { ProfileDialog } from "./components/ProfileDialog";
import { RankingView } from "./components/RankingView";
import { SystemView } from "./components/SystemView";
import { categories, categoryLabel } from "./lib/display";
import type { Note } from "./types/api";

type View = "feed" | "ranking" | "system";

const navigation = [
  { id: "feed" as const, label: "发现笔记", icon: Compass },
  { id: "ranking" as const, label: "今日热榜", icon: Flame },
  { id: "system" as const, label: "系统状态", icon: Activity },
];

function noteIDFromPath(): number | null {
  const match = window.location.pathname.match(/^\/notes\/(\d+)$/);
  if (!match) return null;
  const value = Number(match[1]);
  return Number.isSafeInteger(value) && value > 0 ? value : null;
}

export default function App() {
  const { user, ready } = useAuth();
  const [view, setView] = useState<View>("feed");
  const [category, setCategory] = useState("");
  const [query, setQuery] = useState("");
  const [selectedNoteId, setSelectedNoteId] = useState<number | null>(() => noteIDFromPath());
  const [authOpen, setAuthOpen] = useState(false);
  const [composeOpen, setComposeOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [toast, setToast] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(""), 2600);
    return () => window.clearTimeout(timer);
  }, [toast]);

  useEffect(() => {
    const syncRoute = () => setSelectedNoteId(noteIDFromPath());
    window.addEventListener("popstate", syncRoute);
    return () => window.removeEventListener("popstate", syncRoute);
  }, []);

  function openNote(noteId: number) {
    window.history.pushState({}, "", `/notes/${noteId}`);
    setSelectedNoteId(noteId);
  }

  function closeNote() {
    if (noteIDFromPath()) window.history.pushState({}, "", "/");
    setSelectedNoteId(null);
  }

  function openCompose() {
    if (!user) {
      setAuthOpen(true);
      return;
    }
    setComposeOpen(true);
  }

  function changeView(next: View) {
    setView(next);
    setMobileMenuOpen(false);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  function noteCreated(note: Note) {
    setComposeOpen(false);
    setRefreshKey((key) => key + 1);
    openNote(note.id);
  }

  const pageTitle = view === "feed" ? "发现" : view === "ranking" ? "今日热榜" : "运行状态";

  return (
    <div className="app-shell">
      <aside className={`sidebar ${mobileMenuOpen ? "mobile-open" : ""}`}>
        <div className="brand-lockup">
          <div className="brand-symbol"><Sparkles size={20} /></div>
          <div><strong>NoteInsight</strong><span>Creator Console</span></div>
        </div>
        <nav className="main-nav" aria-label="主导航">
          {navigation.map((item) => {
            const Icon = item.icon;
            return <button key={item.id} className={view === item.id ? "active" : ""} onClick={() => changeView(item.id)}><Icon size={19} /><span>{item.label}</span></button>;
          })}
        </nav>
        <div className="sidebar-spacer" />
        <section className="sidebar-phase">
          <span>PHASE 6B</span>
          <strong>Scale Ready</strong>
          <p>5,000+ 笔记语料已就绪，下一站是证据检索与 Agent。</p>
          <div className="progress-track"><i /></div>
        </section>
        <button className="compose-button" onClick={openCompose}><PenLine size={18} />发布笔记</button>
      </aside>

      {mobileMenuOpen && <button className="sidebar-scrim" aria-label="关闭菜单" onClick={() => setMobileMenuOpen(false)} />}

      <main className="main-column">
        <header className="topbar">
          <button className="icon-button mobile-menu-button" type="button" onClick={() => setMobileMenuOpen(true)} aria-label="打开菜单"><Menu size={21} /></button>
          <div className="mobile-brand">NoteInsight</div>
          {view !== "system" ? (
            <label className="search-field">
              <Search size={18} />
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索全部笔记的标题与正文" />
            </label>
          ) : <div className="topbar-title">{pageTitle}</div>}
          <div className="topbar-actions">
            <button className="icon-button notification-button" type="button" aria-label="通知"><Bell size={19} /><i /></button>
            {!ready ? <div className="avatar avatar-loading" /> : user ? (
              <button className="account-button" type="button" onClick={() => setProfileOpen(true)}><span className="avatar">{(user.nickname || user.username).slice(0, 1).toUpperCase()}</span><span><strong>{user.nickname || user.username}</strong><small>@{user.username}</small></span></button>
            ) : (
              <button className="secondary-button login-button" type="button" onClick={() => setAuthOpen(true)}><LogIn size={17} />登录</button>
            )}
          </div>
        </header>

        <div className="content-area">
          {view !== "system" && (
            <div className="content-header">
              <div>
                <span className="eyebrow">{view === "feed" ? "CONTENT FEED" : "DAILY RANKING"}</span>
                <h1>{pageTitle}</h1>
                <p>{category ? `${categoryLabel(category)}分类` : "全量分类"} · 数据来自当前 Go API</p>
              </div>
              <button className="primary-button desktop-compose" onClick={openCompose}><PenLine size={17} />发布笔记</button>
            </div>
          )}

          {view !== "system" && (
            <div className="category-bar" role="tablist" aria-label="笔记分类">
              {categories.map((item) => <button role="tab" aria-selected={category === item.value} className={category === item.value ? "active" : ""} key={item.value} onClick={() => setCategory(item.value)}>{item.label}</button>)}
            </div>
          )}

          {view === "feed" && <FeedView category={category} query={query} onOpen={openNote} refreshKey={refreshKey} />}
          {view === "ranking" && <RankingView category={category} onOpen={openNote} />}
          {view === "system" && <SystemView />}
        </div>
      </main>

      <nav className="mobile-bottom-nav" aria-label="移动端导航">
        <button className={view === "feed" ? "active" : ""} onClick={() => changeView("feed")}><Compass size={20} /><span>发现</span></button>
        <button className={view === "ranking" ? "active" : ""} onClick={() => changeView("ranking")}><Flame size={20} /><span>热榜</span></button>
        <button className="mobile-compose-action" onClick={openCompose}><PenLine size={21} /><span>发布</span></button>
        <button className={view === "system" ? "active" : ""} onClick={() => changeView("system")}><Activity size={20} /><span>状态</span></button>
      </nav>

      <NoteDetail noteId={selectedNoteId} onClose={closeNote} onNeedAuth={() => setAuthOpen(true)} onToast={setToast} onChanged={() => setRefreshKey((key) => key + 1)} />
      <AuthDialog open={authOpen} onClose={() => setAuthOpen(false)} />
      <ComposeDialog open={composeOpen} onClose={() => setComposeOpen(false)} onCreated={noteCreated} onToast={setToast} />
      <ProfileDialog open={profileOpen} onClose={() => setProfileOpen(false)} onToast={setToast} />
      {toast && <div className="toast" role="status">{toast}</div>}
    </div>
  );
}
