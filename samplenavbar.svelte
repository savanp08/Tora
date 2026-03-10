import { useState } from "react";

/* ── TOKENS ── */
const C = {
  bg:        "#F4F5F7",
  sidebar:   "#FFFFFF",
  nav:       "#FFFFFF",
  card:      "#FFFFFF",
  border:    "#EBECF0",
  borderMid: "#DFE1E6",
  blue:      "#0052CC",
  blueLight: "#DEEBFF",
  blueMid:   "#0065FF",
  text:      "#172B4D",
  textSub:   "#42526E",
  textMuted: "#6B778C",
  textFaint: "#97A0AF",
  green:     "#36B37E",
  greenLight:"#E3FCEF",
  orange:    "#FF8B00",
  orangeLight:"#FFFAE6",
  red:       "#DE350B",
  redLight:  "#FFEBE6",
  purple:    "#6554C0",
  purpleLight:"#EAE6FF",
  teal:      "#00B8D9",
  tealLight: "#E6FCFF",
  yellow:    "#FFC400",
  yellowLight:"#FFFAE6",
  navBorder: "#EBECF0",
};

/* ── DATA ── */
const PROJECTS = [
  { id:"PRJ-1", name:"Nexus Platform v3",  color:"#0052CC", icon:"⬡", starred:true  },
  { id:"PRJ-2", name:"Design System 2.0",  color:"#6554C0", icon:"◈", starred:true  },
  { id:"PRJ-3", name:"Mobile Relaunch",    color:"#36B37E", icon:"◉", starred:false },
  { id:"PRJ-4", name:"Infra Kubernetes",   color:"#00B8D9", icon:"⬟", starred:false },
  { id:"PRJ-5", name:"Marketing Q2",       color:"#FF8B00", icon:"◆", starred:false },
];

const STATUS_DATA = [
  { label:"Done",        count:42, color:"#36B37E" },
  { label:"In progress", count:28, color:"#0052CC" },
  { label:"Sparring",    count:18, color:"#FF8B00" },
  { label:"In review",   count:12, color:"#FFC400" },
  { label:"To do",       count:11, color:"#DFE1E6" },
];

const WORKLOAD = [
  { name:"Unassigned",    pct:19, count:25, color:"#97A0AF" },
  { name:"PRJ-1 Team",    pct:27, count:25, color:"#FF8B00" },
  { name:"PRJ-2 Team",    pct:22, count:20, color:"#DE350B" },
  { name:"PRJ-3 Team",    pct:15, count:14, color:"#0052CC" },
  { name:"PRJ-4 Team",    pct:9,  count:8,  color:"#6554C0" },
  { name:"PRJ-5 Team",    pct:9,  count:8,  color:"#36B37E" },
];

const PRIORITY_DATA = [
  { label:"Critical", count:8,  color:"#DE350B" },
  { label:"High",     count:24, color:"#FF8B00" },
  { label:"Medium",   count:38, color:"#0052CC" },
  { label:"Low",      count:14, color:"#36B37E" },
  { label:"None",     count:6,  color:"#DFE1E6" },
];

const WORK_TYPES = [
  { label:"Feature",    count:34, color:"#0052CC" },
  { label:"Bug",        count:22, color:"#DE350B" },
  { label:"Task",       count:18, color:"#36B37E" },
  { label:"Improvement",count:12, color:"#6554C0" },
  { label:"Research",   count:4,  color:"#00B8D9" },
];

const RECENT_ACTIVITY = [
  { proj:"PRJ-1", id:"#142", action:"moved to",   state:"In Progress", time:"2m ago",  stateColor:"#0052CC" },
  { proj:"PRJ-3", id:"#61",  action:"completed",  state:"Done",        time:"18m ago", stateColor:"#36B37E" },
  { proj:"PRJ-2", id:"#89",  action:"moved to",   state:"In Review",   time:"34m ago", stateColor:"#FFC400" },
  { proj:"PRJ-4", id:"#22",  action:"blocked",    state:"Blocked",     time:"1h ago",  stateColor:"#DE350B" },
  { proj:"PRJ-1", id:"#138", action:"merged →",   state:"Done",        time:"2h ago",  stateColor:"#36B37E" },
];

const NOTIFS = [
  { unread:true,  icon:"💬", title:"New comment on PRJ-1 #142",  time:"3m"  },
  { unread:true,  icon:"⚠️", title:"PRJ-3 deadline in 4 days",   time:"11m" },
  { unread:true,  icon:"🔀", title:"PRJ-2 task moved to Review",  time:"28m" },
  { unread:false, icon:"✅", title:"PRJ-1 #138 merged to main",   time:"2h"  },
  { unread:false, icon:"🚫", title:"PRJ-4 blocked: 5 tasks",      time:"3h"  },
];

const TOP_NAV = ["Your work","Projects","Filters","Dashboards","Apps"];

/* ── DONUT CHART (SVG) ── */
function Donut({ data, size = 180 }) {
  const total = data.reduce((a, d) => a + d.count, 0);
  const cx = size / 2, cy = size / 2;
  const r = size * 0.38, inner = size * 0.24;
  let angle = -90;
  const slices = data.map(d => {
    const deg = (d.count / total) * 360;
    const start = angle;
    angle += deg;
    return { ...d, start, deg };
  });
  function arc(cx, cy, r, startDeg, endDeg) {
    const toRad = d => (d * Math.PI) / 180;
    const x1 = cx + r * Math.cos(toRad(startDeg));
    const y1 = cy + r * Math.sin(toRad(startDeg));
    const x2 = cx + r * Math.cos(toRad(endDeg));
    const y2 = cy + r * Math.sin(toRad(endDeg));
    const large = endDeg - startDeg > 180 ? 1 : 0;
    return `M ${x1} ${y1} A ${r} ${r} 0 ${large} 1 ${x2} ${y2}`;
  }
  return (
    <svg width={size} height={size}>
      {slices.map((s, i) => {
        const end = s.start + s.deg;
        const outerPath = arc(cx, cy, r, s.start, end);
        const innerEnd  = arc(cx, cy, inner, end, s.start);
        const x1o = cx + r     * Math.cos((s.start * Math.PI) / 180);
        const y1o = cy + r     * Math.sin((s.start * Math.PI) / 180);
        const x2i = cx + inner * Math.cos((end     * Math.PI) / 180);
        const y2i = cy + inner * Math.sin((end     * Math.PI) / 180);
        const x1i = cx + inner * Math.cos((s.start * Math.PI) / 180);
        const y1i = cy + inner * Math.sin((s.start * Math.PI) / 180);
        const x2o = cx + r     * Math.cos((end     * Math.PI) / 180);
        const y2o = cy + r     * Math.sin((end     * Math.PI) / 180);
        const large = s.deg > 180 ? 1 : 0;
        const path = [
          `M ${x1o} ${y1o}`,
          `A ${r} ${r} 0 ${large} 1 ${x2o} ${y2o}`,
          `L ${x2i} ${y2i}`,
          `A ${inner} ${inner} 0 ${large} 0 ${x1i} ${y1i}`,
          "Z"
        ].join(" ");
        return <path key={i} d={path} fill={s.color} stroke="#fff" strokeWidth={2}/>;
      })}
      <text x={cx} y={cy - 8} textAnchor="middle" fontSize={28} fontWeight={700} fill={C.text}
        fontFamily="'Inter','Segoe UI',sans-serif">{total}</text>
      <text x={cx} y={cy + 14} textAnchor="middle" fontSize={12} fill={C.textMuted}
        fontFamily="'Inter','Segoe UI',sans-serif">Total</text>
    </svg>
  );
}

/* ── HORIZONTAL BAR ── */
function HBar({ pct, color }) {
  return (
    <div style={{ flex:1, height:8, background:C.border, borderRadius:99, overflow:"hidden" }}>
      <div style={{ width:`${pct}%`, height:"100%", background:color, borderRadius:99,
        transition:"width 0.8s cubic-bezier(.4,0,.2,1)" }}/>
    </div>
  );
}

/* ── SEGMENTED BAR ── */
function SegBar({ data }) {
  const total = data.reduce((a,d)=>a+d.count,0);
  return (
    <div style={{ display:"flex", height:12, borderRadius:99, overflow:"hidden", gap:2 }}>
      {data.map((d,i) => (
        <div key={i} style={{ flex:d.count/total, background:d.color, minWidth:4,
          transition:"flex 0.8s cubic-bezier(.4,0,.2,1)" }}/>
      ))}
    </div>
  );
}

/* ── STAT CARD SVG ICONS ── */
const STAT_ICONS = {
  done: (color) => (
    <svg width={20} height={20} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
      <polyline points="20 6 9 17 4 12"/>
    </svg>
  ),
  updated: (color) => (
    <svg width={20} height={20} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
    </svg>
  ),
  new: (color) => (
    <svg width={20} height={20} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={2.5} strokeLinecap="round">
      <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
    </svg>
  ),
  due: (color) => (
    <svg width={20} height={20} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="18" rx="2" ry="2"/>
      <line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/>
      <line x1="3" y1="10" x2="21" y2="10"/>
    </svg>
  ),
};

/* ── STAT CARD (top row) — Jira-accurate ── */
function StatCard({ iconKey, iconBg, iconColor, value, label, sublabel, color }) {
  return (
    <div style={{
      background: C.card,
      border: `1px solid ${C.border}`,
      borderRadius: 8,
      padding: "20px 20px",
      display: "flex",
      alignItems: "center",
      gap: 16,
      flex: 1,
      boxShadow: "0 1px 2px rgba(9,30,66,.06)",
      cursor: "pointer",
      transition: "box-shadow 0.15s, border-color 0.15s",
    }}
    onMouseEnter={e => {
      e.currentTarget.style.boxShadow = "0 4px 12px rgba(9,30,66,.12)";
      e.currentTarget.style.borderColor = "#C1C7D0";
    }}
    onMouseLeave={e => {
      e.currentTarget.style.boxShadow = "0 1px 2px rgba(9,30,66,.06)";
      e.currentTarget.style.borderColor = C.border;
    }}>
      {/* Circle icon — matches Jira's subtle outlined circle */}
      <div style={{
        width: 44,
        height: 44,
        borderRadius: "50%",
        background: iconBg,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        flexShrink: 0,
      }}>
        {STAT_ICONS[iconKey]?.(iconColor)}
      </div>

      {/* Text block */}
      <div>
        {/* Number + label inline, like Jira: "12 done" */}
        <div style={{ display: "flex", alignItems: "baseline", gap: 6, lineHeight: 1 }}>
          <span style={{
            fontSize: 26,
            fontWeight: 700,
            color: color,
            letterSpacing: "-0.5px",
            lineHeight: 1,
          }}>{value}</span>
          <span style={{
            fontSize: 15,
            fontWeight: 600,
            color: C.text,
            lineHeight: 1,
          }}>{label}</span>
        </div>
        {/* Subtitle */}
        <div style={{
          fontSize: 12,
          color: C.textMuted,
          marginTop: 5,
          fontWeight: 400,
        }}>{sublabel}</div>
      </div>
    </div>
  );
}

/* ── SECTION CARD ── */
function Card({ title, subtitle, linkText, children, style={} }) {
  return (
    <div style={{ background:C.card, border:`1px solid ${C.border}`, borderRadius:8,
      boxShadow:"0 1px 2px rgba(9,30,66,.08)", overflow:"hidden", ...style }}>
      <div style={{ padding:"20px 24px 0" }}>
        <div style={{ fontSize:16, fontWeight:700, color:C.text }}>{title}</div>
        {subtitle && (
          <div style={{ fontSize:13, color:C.textMuted, marginTop:4 }}>
            {subtitle}{" "}
            {linkText && <span style={{ color:C.blue, cursor:"pointer" }}>{linkText}</span>}
          </div>
        )}
      </div>
      <div style={{ padding:"16px 24px 24px" }}>{children}</div>
    </div>
  );
}

/* ── MAIN ── */
export default function JiraDashboard() {
  const [activeNav, setActiveNav] = useState("Projects");
  const [activeTab, setActiveTab] = useState("Summary");
  const [activeProject, setActiveProject] = useState(PROJECTS[0]);
  const [notifOpen, setNotifOpen] = useState(false);
  const [sidebarSection, setSidebarSection] = useState("overview");
  const unread = NOTIFS.filter(n=>n.unread).length;
  const total = STATUS_DATA.reduce((a,d)=>a+d.count,0);

  return (
    <div style={{ display:"flex", flexDirection:"column", height:"100vh", width:"100vw",
      background:C.bg, fontFamily:"'Inter','Segoe UI',-apple-system,sans-serif",
      color:C.text, overflow:"hidden" }}>
      <style>{`
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap');
        * { box-sizing:border-box; margin:0; padding:0; }
        ::-webkit-scrollbar { width:6px; }
        ::-webkit-scrollbar-track { background:transparent; }
        ::-webkit-scrollbar-thumb { background:#DFE1E6; border-radius:99px; }
        ::-webkit-scrollbar-thumb:hover { background:#C1C7D0; }
        button { font-family:inherit; cursor:pointer; border:none; background:none; }
      `}</style>

      {/* ── TOP NAV ── */}
      <div style={{ height:56, background:C.nav, borderBottom:`1px solid ${C.navBorder}`,
        display:"flex", alignItems:"center", paddingInline:20, gap:0,
        flexShrink:0, zIndex:100, boxShadow:"0 1px 0 rgba(9,30,66,.13)" }}>
        {/* Logo */}
        <div style={{ display:"flex", alignItems:"center", gap:8, marginRight:24 }}>
          <div style={{ width:28, height:28, borderRadius:6,
            background:"linear-gradient(135deg,#0052CC,#0065FF)",
            display:"flex", alignItems:"center", justifyContent:"center",
            fontSize:14, fontWeight:900, color:"#fff" }}>P</div>
          <span style={{ fontSize:14, fontWeight:700, color:C.text }}>ProjectOS</span>
        </div>

        {/* Nav items */}
        {TOP_NAV.map(nav => (
          <button key={nav} onClick={()=>setActiveNav(nav)} style={{
            padding:"0 12px", height:56, fontSize:13, fontWeight:500,
            color: activeNav===nav ? C.blue : C.textSub,
            borderBottom: activeNav===nav ? `2px solid ${C.blue}` : "2px solid transparent",
            background:"none", whiteSpace:"nowrap",
            transition:"color 0.1s",
          }}
          onMouseEnter={e=>{ if(activeNav!==nav) e.currentTarget.style.color=C.text; }}
          onMouseLeave={e=>{ if(activeNav!==nav) e.currentTarget.style.color=C.textSub; }}>
            {nav} {["Your work","Projects","Filters","Dashboards","Apps"].includes(nav) && 
              <span style={{ fontSize:10, color:C.textFaint }}>⌄</span>}
          </button>
        ))}

        <div style={{ flex:1 }}/>

        {/* Search */}
        <div style={{ display:"flex", alignItems:"center", gap:8, background:C.bg,
          border:`1px solid ${C.borderMid}`, borderRadius:4, padding:"6px 12px", width:200,
          marginRight:12, cursor:"text" }}>
          <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke={C.textMuted}
            strokeWidth={2} strokeLinecap="round"><circle cx={11} cy={11} r={8}/><path d="m21 21-4.35-4.35"/></svg>
          <span style={{ fontSize:13, color:C.textFaint }}>Search</span>
        </div>

        {/* Icons */}
        <div style={{ display:"flex", alignItems:"center", gap:4 }}>
          {/* Notif bell */}
          <div style={{ position:"relative" }}>
            <button onClick={()=>setNotifOpen(!notifOpen)}
              style={{ width:34, height:34, borderRadius:"50%", display:"flex",
                alignItems:"center", justifyContent:"center", position:"relative",
                background:notifOpen?C.blueLight:"transparent",
                transition:"background 0.1s" }}
              onMouseEnter={e=>e.currentTarget.style.background=C.bg}
              onMouseLeave={e=>e.currentTarget.style.background=notifOpen?C.blueLight:"transparent"}>
              <svg width={18} height={18} viewBox="0 0 24 24" fill="none"
                stroke={C.textSub} strokeWidth={2} strokeLinecap="round">
                <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0"/>
              </svg>
              {unread > 0 && (
                <div style={{ position:"absolute", top:4, right:4, width:8, height:8,
                  background:C.blue, borderRadius:"50%", border:"2px solid #fff" }}/>
              )}
            </button>
            {notifOpen && (
              <div style={{ position:"absolute", right:0, top:42, width:340, background:"#fff",
                border:`1px solid ${C.border}`, borderRadius:8, zIndex:999,
                boxShadow:"0 8px 32px rgba(9,30,66,.18)", overflow:"hidden" }}>
                <div style={{ padding:"14px 16px", borderBottom:`1px solid ${C.border}`,
                  display:"flex", justifyContent:"space-between", alignItems:"center" }}>
                  <span style={{ fontSize:14, fontWeight:700, color:C.text }}>Notifications</span>
                  <span style={{ fontSize:12, color:C.blue, cursor:"pointer" }}>Mark all read</span>
                </div>
                {NOTIFS.map((n,i)=>(
                  <div key={i} style={{ display:"flex", gap:12, padding:"12px 16px",
                    background:n.unread?"#F4F5F7":"#fff",
                    borderBottom:`1px solid ${C.border}`, cursor:"pointer",
                    transition:"background 0.1s" }}
                    onMouseEnter={e=>e.currentTarget.style.background="#EBECF0"}
                    onMouseLeave={e=>e.currentTarget.style.background=n.unread?"#F4F5F7":"#fff"}>
                    <span style={{ fontSize:18, flexShrink:0 }}>{n.icon}</span>
                    <div style={{ flex:1 }}>
                      <div style={{ fontSize:13, color:C.text, fontWeight:n.unread?600:400 }}>{n.title}</div>
                      <div style={{ fontSize:11, color:C.textFaint, marginTop:2 }}>{n.time} ago</div>
                    </div>
                    {n.unread && <div style={{ width:8, height:8, borderRadius:"50%",
                      background:C.blue, flexShrink:0, marginTop:4 }}/>}
                  </div>
                ))}
              </div>
            )}
          </div>
          {["?","⚙"].map((ic,i)=>(
            <button key={i} style={{ width:34, height:34, borderRadius:"50%", fontSize:16,
              color:C.textSub, display:"flex", alignItems:"center", justifyContent:"center",
              transition:"background 0.1s" }}
              onMouseEnter={e=>e.currentTarget.style.background=C.bg}
              onMouseLeave={e=>e.currentTarget.style.background="transparent"}>{ic}</button>
          ))}
          {/* Avatar */}
          <div style={{ width:32, height:32, borderRadius:"50%", marginLeft:4,
            background:"linear-gradient(135deg,#0052CC,#6554C0)",
            display:"flex", alignItems:"center", justifyContent:"center",
            fontSize:12, fontWeight:700, color:"#fff", cursor:"pointer" }}>P</div>
        </div>
      </div>

      {/* ── BODY ── */}
      <div style={{ flex:1, display:"flex", overflow:"hidden" }}>

        {/* ── LEFT SIDEBAR ── */}
        <div style={{ width:260, background:C.sidebar, borderRight:`1px solid ${C.border}`,
          display:"flex", flexDirection:"column", flexShrink:0, overflowY:"auto", padding:"12px 0" }}>

          {/* Overview section */}
          <div style={{ padding:"4px 16px 8px" }}>
            <div style={{ display:"flex", justifyContent:"space-between", alignItems:"center" }}>
              <span style={{ fontSize:12, fontWeight:600, color:C.textMuted, textTransform:"uppercase",
                letterSpacing:"0.6px" }}>Overviews</span>
              <button style={{ fontSize:18, color:C.textMuted, lineHeight:1 }}>+</button>
            </div>
            <div style={{ marginTop:6 }}>
              <div style={{ display:"flex", alignItems:"center", gap:10, padding:"6px 10px",
                borderRadius:4, background:C.blueLight, cursor:"pointer" }}>
                <span style={{ fontSize:14, color:C.blue }}>◉</span>
                <span style={{ fontSize:13, fontWeight:500, color:C.blue }}>All Projects</span>
              </div>
            </div>
          </div>

          <div style={{ height:1, background:C.border, margin:"8px 0" }}/>

          {/* Projects section */}
          <div style={{ padding:"4px 16px 8px" }}>
            <div style={{ display:"flex", justifyContent:"space-between", alignItems:"center", marginBottom:6 }}>
              <span style={{ fontSize:12, fontWeight:600, color:C.textMuted, textTransform:"uppercase",
                letterSpacing:"0.6px" }}>Projects</span>
              <button style={{ fontSize:18, color:C.textMuted, lineHeight:1 }}>+</button>
            </div>

            <div style={{ fontSize:11, fontWeight:600, color:C.textFaint, marginBottom:4,
              display:"flex", alignItems:"center", gap:4 }}>
              <span>⌄</span> STARRED
            </div>
            {PROJECTS.filter(p=>p.starred).map(p=>(
              <div key={p.id}
                style={{ display:"flex", alignItems:"center", gap:10, padding:"6px 10px",
                  borderRadius:4, cursor:"pointer", marginBottom:2,
                  background: activeProject.id===p.id ? C.blueLight : "transparent" }}
                onMouseEnter={e=>{ if(activeProject.id!==p.id) e.currentTarget.style.background=C.bg; }}
                onMouseLeave={e=>{ if(activeProject.id!==p.id) e.currentTarget.style.background="transparent"; }}
                onClick={()=>setActiveProject(p)}>
                <div style={{ width:20, height:20, borderRadius:4, background:p.color,
                  display:"flex", alignItems:"center", justifyContent:"center",
                  fontSize:11, color:"#fff", flexShrink:0 }}>{p.icon}</div>
                <span style={{ fontSize:13, fontWeight:500,
                  color:activeProject.id===p.id?C.blue:C.text,
                  overflow:"hidden", textOverflow:"ellipsis", whiteSpace:"nowrap" }}>{p.name}</span>
              </div>
            ))}

            <div style={{ fontSize:11, fontWeight:600, color:C.textFaint, margin:"8px 0 4px",
              display:"flex", alignItems:"center", gap:4 }}>
              <span>⌄</span> RECENT
            </div>
            {PROJECTS.filter(p=>!p.starred).map(p=>(
              <div key={p.id}
                style={{ display:"flex", alignItems:"center", gap:10, padding:"6px 10px",
                  borderRadius:4, cursor:"pointer", marginBottom:2,
                  background: activeProject.id===p.id ? C.blueLight : "transparent" }}
                onMouseEnter={e=>{ if(activeProject.id!==p.id) e.currentTarget.style.background=C.bg; }}
                onMouseLeave={e=>{ if(activeProject.id!==p.id) e.currentTarget.style.background="transparent"; }}
                onClick={()=>setActiveProject(p)}>
                <div style={{ width:20, height:20, borderRadius:4, background:p.color,
                  display:"flex", alignItems:"center", justifyContent:"center",
                  fontSize:11, color:"#fff", flexShrink:0 }}>{p.icon}</div>
                <span style={{ fontSize:13, fontWeight:500,
                  color:activeProject.id===p.id?C.blue:C.text,
                  overflow:"hidden", textOverflow:"ellipsis", whiteSpace:"nowrap" }}>{p.name}</span>
              </div>
            ))}

            <button style={{ fontSize:13, color:C.blue, padding:"6px 10px", textAlign:"left",
              width:"100%", display:"block", marginTop:4 }}>
              View all projects
            </button>
          </div>

          <div style={{ height:1, background:C.border, margin:"8px 0" }}/>

          <div style={{ padding:"4px 16px" }}>
            <div style={{ display:"flex", alignItems:"center", gap:10, padding:"6px 10px",
              borderRadius:4, cursor:"pointer" }}
              onMouseEnter={e=>e.currentTarget.style.background=C.bg}
              onMouseLeave={e=>e.currentTarget.style.background="transparent"}>
              <span style={{ fontSize:16 }}>📣</span>
              <span style={{ fontSize:13, color:C.textSub }}>Give feedback</span>
            </div>
          </div>
        </div>

        {/* ── MAIN CONTENT ── */}
        <div style={{ flex:1, overflowY:"auto" }}>
          {/* Project header */}
          <div style={{ background:C.sidebar, borderBottom:`1px solid ${C.border}`,
            padding:"0 32px" }}>
            <div style={{ display:"flex", alignItems:"center", gap:10, paddingTop:16, paddingBottom:10 }}>
              <div style={{ width:24, height:24, borderRadius:5, background:activeProject.color,
                display:"flex", alignItems:"center", justifyContent:"center",
                fontSize:12, color:"#fff" }}>{activeProject.icon}</div>
              <span style={{ fontSize:18, fontWeight:700, color:C.text }}>{activeProject.name}</span>
              <span style={{ fontSize:12, color:C.textFaint }}>⌄</span>
              <div style={{ display:"flex", gap:4, marginLeft:8 }}>
                {["⚙","👥","🔗"].map((ic,i)=>(
                  <button key={i} style={{ width:26, height:26, borderRadius:4, fontSize:13,
                    color:C.textSub, display:"flex", alignItems:"center", justifyContent:"center",
                    transition:"background 0.1s" }}
                    onMouseEnter={e=>e.currentTarget.style.background=C.bg}
                    onMouseLeave={e=>e.currentTarget.style.background="transparent"}>{ic}</button>
                ))}
              </div>
            </div>
            {/* Tabs */}
            <div style={{ display:"flex", gap:0 }}>
              {["Summary","Calendar","Timeline","Board"].map(tab=>(
                <button key={tab} onClick={()=>setActiveTab(tab)} style={{
                  padding:"8px 16px", fontSize:14, fontWeight:activeTab===tab?600:400,
                  color:activeTab===tab?C.blue:C.textSub,
                  borderBottom:activeTab===tab?`2px solid ${C.blue}`:"2px solid transparent",
                  marginBottom:-1, background:"none", transition:"color 0.1s",
                }}
                onMouseEnter={e=>{ if(activeTab!==tab) e.currentTarget.style.color=C.text; }}
                onMouseLeave={e=>{ if(activeTab!==tab) e.currentTarget.style.color=C.textSub; }}>
                  {tab}
                </button>
              ))}
            </div>
          </div>

          {/* Page content */}
          <div style={{ padding:"32px 32px 48px", maxWidth:1100 }}>
            {/* Greeting */}
            <div style={{ textAlign:"center", marginBottom:28 }}>
              <div style={{ fontSize:22, fontWeight:700, color:C.text }}>
                🌤️ Welcome to {activeProject.name}
              </div>
              <div style={{ fontSize:14, color:C.textMuted, marginTop:6 }}>
                Here's a summary of all open work across this project.
              </div>
            </div>

            {/* ── STAT CARDS ROW ── */}
            <div style={{ display:"grid", gridTemplateColumns:"repeat(4,1fr)", gap:16, marginBottom:24 }}>
              <StatCard iconKey="done"    iconBg="#DCFFF0" iconColor="#36B37E" value={42}
                label="done"    sublabel="in the last 7 days 🎉" color={C.green}/>
              <StatCard iconKey="updated" iconBg="#DEEBFF" iconColor="#0052CC" value={10}
                label="updated" sublabel="in the last 7 days"    color={C.blue}/>
              <StatCard iconKey="new"     iconBg="#EAE6FF" iconColor="#6554C0" value={6}
                label="new"     sublabel="in the last 7 days"    color={C.purple}/>
              <StatCard iconKey="due"     iconBg="#FFEBE6" iconColor="#DE350B" value={3}
                label="due"     sublabel="in the next 7 days"    color={C.red}/>
            </div>

            {/* ── ROW 1: Status Summary + Project Workload ── */}
            <div style={{ display:"grid", gridTemplateColumns:"1fr 1fr", gap:20, marginBottom:20 }}>
              {/* Status Summary */}
              <Card title="Status summary"
                subtitle="A snapshot of your project status."
                linkText="View more details.">
                <div style={{ display:"flex", alignItems:"center", gap:24 }}>
                  <Donut data={STATUS_DATA} size={176}/>
                  <div style={{ flex:1 }}>
                    {STATUS_DATA.map((s,i)=>(
                      <div key={i} style={{ display:"flex", justifyContent:"space-between",
                        alignItems:"center", padding:"7px 0",
                        borderBottom:i<STATUS_DATA.length-1?`1px solid ${C.border}`:"none" }}>
                        <div style={{ display:"flex", alignItems:"center", gap:8 }}>
                          <div style={{ width:12, height:12, borderRadius:3, background:s.color, flexShrink:0 }}/>
                          <span style={{ fontSize:13, color:C.text }}>{s.label}</span>
                        </div>
                        <span style={{ fontSize:13, fontWeight:700, color:C.blue }}>{s.count}</span>
                      </div>
                    ))}
                    <div style={{ display:"flex", justifyContent:"space-between",
                      alignItems:"center", paddingTop:8, marginTop:2 }}>
                      <span style={{ fontSize:13, fontWeight:700, color:C.text }}>Total</span>
                      <span style={{ fontSize:13, fontWeight:700, color:C.blue }}>{total}</span>
                    </div>
                  </div>
                </div>
              </Card>

              {/* Project Workload */}
              <Card title="Project workload"
                subtitle="Oversee the capacity across all projects."
                linkText="Re-assign tasks across projects.">
                <div style={{ display:"grid", gridTemplateColumns:"1fr 1fr auto", gap:"0 12px",
                  marginBottom:6 }}>
                  <span style={{ fontSize:12, fontWeight:600, color:C.textMuted }}>Project</span>
                  <span style={{ fontSize:12, fontWeight:600, color:C.textMuted }}>Work distribution</span>
                  <span style={{ fontSize:12, fontWeight:600, color:C.textMuted }}>Count</span>
                </div>
                {WORKLOAD.map((w,i)=>(
                  <div key={i} style={{ display:"grid", gridTemplateColumns:"1fr 1fr auto",
                    gap:"0 12px", alignItems:"center", padding:"7px 0",
                    borderBottom:i<WORKLOAD.length-1?`1px solid ${C.border}`:"none" }}>
                    <span style={{ fontSize:13, color:C.text, overflow:"hidden",
                      textOverflow:"ellipsis", whiteSpace:"nowrap" }}>{w.name}</span>
                    <div style={{ display:"flex", alignItems:"center", gap:8 }}>
                      <HBar pct={w.pct} color={w.color}/>
                      <span style={{ fontSize:12, color:C.textMuted, width:30, textAlign:"right",
                        flexShrink:0 }}>{w.pct}%</span>
                    </div>
                    <span style={{ fontSize:13, fontWeight:700, color:C.blue,
                      textAlign:"right" }}>{w.count}</span>
                  </div>
                ))}
              </Card>
            </div>

            {/* ── ROW 2: Priority Breakdown + Types of Work ── */}
            <div style={{ display:"grid", gridTemplateColumns:"1fr 1fr", gap:20, marginBottom:20 }}>
              {/* Priority Breakdown */}
              <Card title="Priority breakdown"
                subtitle="A holistic view of how work is being prioritized."
                linkText="See what your project's been working on.">
                <div style={{ marginBottom:14 }}>
                  <SegBar data={PRIORITY_DATA}/>
                </div>
                {PRIORITY_DATA.map((p,i)=>(
                  <div key={i} style={{ display:"flex", justifyContent:"space-between",
                    alignItems:"center", padding:"7px 0",
                    borderBottom:i<PRIORITY_DATA.length-1?`1px solid ${C.border}`:"none" }}>
                    <div style={{ display:"flex", alignItems:"center", gap:8 }}>
                      <div style={{ width:12, height:12, borderRadius:3, background:p.color, flexShrink:0 }}/>
                      <span style={{ fontSize:13, color:C.text }}>{p.label}</span>
                    </div>
                    <span style={{ fontSize:13, fontWeight:700, color:C.blue }}>{p.count}</span>
                  </div>
                ))}
              </Card>

              {/* Types of Work */}
              <Card title="Types of work"
                subtitle="A breakdown of items by their types."
                linkText="View all items.">
                <div style={{ marginBottom:14 }}>
                  <SegBar data={WORK_TYPES}/>
                </div>
                {WORK_TYPES.map((w,i)=>(
                  <div key={i} style={{ display:"flex", justifyContent:"space-between",
                    alignItems:"center", padding:"7px 0",
                    borderBottom:i<WORK_TYPES.length-1?`1px solid ${C.border}`:"none" }}>
                    <div style={{ display:"flex", alignItems:"center", gap:8 }}>
                      <div style={{ width:12, height:12, borderRadius:3, background:w.color, flexShrink:0 }}/>
                      <span style={{ fontSize:13, color:C.text }}>{w.label}</span>
                    </div>
                    <span style={{ fontSize:13, fontWeight:700, color:C.blue }}>{w.count}</span>
                  </div>
                ))}
              </Card>
            </div>

            {/* ── ROW 3: Recent Activity (full width) ── */}
            <Card title="Recent activity"
              subtitle="Latest changes across all projects in this overview."
              linkText="View full log.">
              <div style={{ display:"grid", gridTemplateColumns:"80px 80px 1fr 100px 80px",
                gap:"0 16px", marginBottom:8 }}>
                {["Project","Task ID","Action","Status","When"].map(h=>(
                  <span key={h} style={{ fontSize:12, fontWeight:600, color:C.textMuted }}>{h}</span>
                ))}
              </div>
              {RECENT_ACTIVITY.map((a,i)=>(
                <div key={i} style={{ display:"grid", gridTemplateColumns:"80px 80px 1fr 100px 80px",
                  gap:"0 16px", alignItems:"center", padding:"8px 0",
                  borderTop:`1px solid ${C.border}`, cursor:"pointer",
                  transition:"background 0.1s" }}
                  onMouseEnter={e=>e.currentTarget.style.background="#F4F5F7"}
                  onMouseLeave={e=>e.currentTarget.style.background="transparent"}>
                  <span style={{ fontSize:13, fontWeight:600,
                    color: PROJECTS.find(p=>p.id===a.proj)?.color || C.text }}>{a.proj}</span>
                  <span style={{ fontSize:13, color:C.blue, cursor:"pointer" }}>{a.id}</span>
                  <span style={{ fontSize:13, color:C.textSub }}>{a.action}</span>
                  <span style={{ fontSize:12, fontWeight:600, padding:"2px 8px", borderRadius:3,
                    color:a.stateColor, background:a.stateColor+"18",
                    display:"inline-block" }}>{a.state}</span>
                  <span style={{ fontSize:12, color:C.textFaint }}>{a.time}</span>
                </div>
              ))}
            </Card>
          </div>
        </div>
      </div>
    </div>
  );
}
