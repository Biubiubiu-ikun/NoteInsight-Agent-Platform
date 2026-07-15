import { Activity, CheckCircle2, CircleAlert, Database, Radio, RefreshCcw, Server, TimerReset } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

interface RuntimeStatus {
  name: string;
  detail: string;
  ok: boolean;
  icon: React.ReactNode;
}

interface PipelineMetrics {
  outboxPending: number | null;
  outboxFailed: number | null;
  oldestAge: number | null;
  jsPending: number | null;
  jsAckPending: number | null;
  natsConnected: number | null;
}

function metric(text: string, name: string, labels = ""): number | null {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const labelPattern = labels ? `\\{[^}]*${labels}[^}]*\\}` : "(?:\\{[^}]*\\})?";
  const match = text.match(new RegExp(`^${escaped}${labelPattern}\\s+([0-9.eE+-]+)$`, "m"));
  return match ? Number(match[1]) : null;
}

async function probe(name: string, path: string, icon: React.ReactNode): Promise<RuntimeStatus> {
  try {
    const response = await fetch(path);
    if (!response.ok) throw new Error(String(response.status));
    return { name, detail: "ready", ok: true, icon };
  } catch {
    return { name, detail: "unavailable", ok: false, icon };
  }
}

export function SystemView() {
  const [statuses, setStatuses] = useState<RuntimeStatus[]>([]);
  const [metrics, setMetrics] = useState<PipelineMetrics>({ outboxPending: null, outboxFailed: null, oldestAge: null, jsPending: null, jsAckPending: null, natsConnected: null });
  const [checkedAt, setCheckedAt] = useState<Date | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    const [api, worker, nats, workerMetrics] = await Promise.all([
      probe("Go API", "/backend-runtime/ready", <Server size={21} />),
      probe("Event Worker", "/worker-runtime/ready", <Activity size={21} />),
      probe("NATS JetStream", "/nats-runtime/varz", <Radio size={21} />),
      fetch("/worker-runtime/metrics").then((response) => response.ok ? response.text() : "").catch(() => ""),
    ]);
    setStatuses([api, worker, nats]);
    setMetrics({
      outboxPending: metric(workerMetrics, "outbox_events", 'status="pending"'),
      outboxFailed: metric(workerMetrics, "outbox_events", 'status="failed"'),
      oldestAge: metric(workerMetrics, "outbox_oldest_unsent_age_seconds"),
      jsPending: metric(workerMetrics, "jetstream_consumer_pending_messages"),
      jsAckPending: metric(workerMetrics, "jetstream_consumer_ack_pending_messages"),
      natsConnected: metric(workerMetrics, "nats_connected"),
    });
    setCheckedAt(new Date());
    setLoading(false);
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  const metricCards = [
    { label: "Outbox 待发布", value: metrics.outboxPending, suffix: "events", icon: <Database size={19} />, good: metrics.outboxPending === 0 },
    { label: "Outbox 失败", value: metrics.outboxFailed, suffix: "events", icon: <CircleAlert size={19} />, good: metrics.outboxFailed === 0 },
    { label: "最老未发送", value: metrics.oldestAge, suffix: "seconds", icon: <TimerReset size={19} />, good: metrics.oldestAge === 0 },
    { label: "JetStream 待投递", value: metrics.jsPending, suffix: "messages", icon: <Radio size={19} />, good: metrics.jsPending === 0 },
    { label: "消费者待确认", value: metrics.jsAckPending, suffix: "messages", icon: <Activity size={19} />, good: metrics.jsAckPending === 0 },
    { label: "NATS 连接", value: metrics.natsConnected, suffix: metrics.natsConnected === 1 ? "connected" : "disconnected", icon: <CheckCircle2 size={19} />, good: metrics.natsConnected === 1 },
  ];

  return (
    <div className="system-view">
      <div className="view-intro"><div><span className="eyebrow">RUNTIME</span><h1>系统运行状态</h1><p>直接读取 API、Worker、NATS 与 Prometheus 指标，不写入业务数据。</p></div><button className="secondary-button" onClick={refresh} disabled={loading}><RefreshCcw className={loading ? "spin" : ""} size={16} />刷新</button></div>
      <section className="runtime-strip" aria-label="服务可用性">
        {statuses.map((status) => <article key={status.name} className={status.ok ? "runtime-ok" : "runtime-bad"}><div className="runtime-icon">{status.icon}</div><div><strong>{status.name}</strong><span>{status.detail}</span></div><i>{status.ok ? <CheckCircle2 size={18} /> : <CircleAlert size={18} />}</i></article>)}
      </section>
      <div className="section-heading status-heading"><div><span>EVENT PIPELINE</span><h2>异步链路快照</h2></div><small>{checkedAt ? `检查于 ${checkedAt.toLocaleTimeString("zh-CN")}` : "等待检查"}</small></div>
      <section className="metric-grid">
        {metricCards.map((item) => <article key={item.label}><div className={item.good ? "metric-icon good" : "metric-icon warn"}>{item.icon}</div><span>{item.label}</span><strong>{item.value ?? "-"}</strong><small>{item.suffix}</small></article>)}
      </section>
      <section className="phase-preview">
        <span className="eyebrow">NEXT SURFACE</span>
        <h2>Evidence Store / RAG / Agent</h2>
        <p>当前阶段保留入口位置。下一阶段接入检索和 Agent 后，这里将展示证据片段、来源笔记、置信度与执行轨迹。</p>
        <button className="secondary-button" type="button" disabled>Phase 7 开放</button>
      </section>
    </div>
  );
}
