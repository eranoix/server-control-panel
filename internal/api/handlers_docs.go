package api

import (
	"bytes"
	"net/http"
	"os"
	"regexp"

	"server-control-panel/internal/webassets"
)

// handleDocsReport serves the HTML technical report INSIDE the panel,
// restricted to the primary user via mustPrimary. The HTML is embedded in an FS
// separate from web/* (webassets.docsFS), so it is NOT reachable through the
// public FileServer — the only path is this gated route.
//
// IMPORTANT (comment armor against /heal): this handler is registered in
// registerRoutes ("/_docs"). Removing it breaks TestSmokeDocsGated. Do not delete
// it without updating the test and the route.
func (r *Router) handleDocsReport(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return // mustPrimary already wrote 401/403
	}
	html, err := webassets.DocsReport()
	if err != nil {
		http.Error(w, "technical report unavailable (not embedded in this build)", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write(html)
}

// graphViews points at the two renderings of the same graph produced by Graphify:
// force (force-directed graph, vis-network) and callflow (architecture/call flow, Mermaid).
type graphViews struct {
	force    string
	callflow string
}

// knowledgeGraphs maps a project name -> its views. A FIXED whitelist — no
// user-supplied path: only these known destinations are servable via
// /_graph, which eliminates path traversal.
var knowledgeGraphs = map[string]graphViews{
	"vps-manager": {
		"/opt/panel/graphify-out/graph.html",
		"/opt/panel/graphify-out/vps-manager-callflow.html",
	},
	"northwind-web": {
		"/root/projetos/northwind-web/graphify-out/graph.html",
		"/root/projetos/northwind-web/graphify-out/northwind-web-callflow.html",
	},
	"acme-booking": {
		"/root/projetos/acme-booking/graphify-out/graph.html",
		"/root/projetos/acme-booking/graphify-out/acme-booking-callflow.html",
	},
	"private-ai-api": {
		"/opt/private-ai-api/graphify-out/graph.html",
		"/opt/private-ai-api/graphify-out/private-ai-api-callflow.html",
	},
	"claude-router": {
		"/opt/claude-router/graphify-out/graph.html",
		"/opt/claude-router/graphify-out/claude-router-callflow.html",
	},
	"hub": {
		"/opt/panel/data/knowledge/hub/graphify-out/graph.html",
		"/opt/panel/data/knowledge/hub/graphify-out/hub-callflow.html",
	},
}

// cdnRewrites rewrites the external-CDN <script src> tags (which the CSP script-src
// 'self' blocks) to the locally vendored copies (same-origin, /vendor/),
// served by the embedded FileServer. Without this the views do not render in the panel.
var cdnRewrites = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`https://unpkg\.com/vis-network@[^"'\s]*`), "/vendor/vis-network/vis-network.min.js"},
	{regexp.MustCompile(`https://cdn\.jsdelivr\.net/npm/mermaid@[^"'\s]*`), "/vendor/mermaid/mermaid.min.js"},
}

// handleKnowledgeGraph serves, gated to the primary user (mustPrimary), one of the
// views (force|callflow) of a project's graph. It reads from disk — always
// fresh, no re-embed — rewrites the CDNs to the local vendor copies (CSP), and on the
// force view injects the organization panel (noise filters + collapse communities
// + freeze layout). Mirrors handleDocsReport.
//
// IMPORTANT (comment armor against /heal): registered in registerRoutes
// ("/_graph") under auth.Middleware, gated by mustPrimary. Removing it breaks
// TestSmokeGraphGated. Do not delete it without updating the test and the route.
func (r *Router) handleKnowledgeGraph(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return // mustPrimary already wrote 401/403
	}
	gv, ok := knowledgeGraphs[req.URL.Query().Get("project")]
	if !ok {
		http.Error(w, "unknown graph project", http.StatusNotFound)
		return
	}
	view := req.URL.Query().Get("view")
	var path string
	switch view {
	case "callflow":
		path = gv.callflow
	default:
		view, path = "force", gv.force
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "view unavailable — run scripts/knowledge/rebuild-all.sh", http.StatusNotFound)
		return
	}
	for _, rw := range cdnRewrites {
		data = rw.re.ReplaceAll(data, []byte(rw.repl))
	}
	if view == "force" {
		data = bytes.Replace(data, []byte("</body>"), []byte(graphControlsHTML+"</body>"), 1)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write(data)
}

// graphControlsHTML is injected before </body> on the force view. In the free
// space of graph.html's sidebar it adds controls to ORGANIZE the nodes:
// hide tests / leaves / anything below degree N, collapse by community (vis-network
// cluster) and freeze the layout. It uses the nodesDS/edgesDS/network globals
// (classic scripts => shared global lexical scope), with a guard.
const graphControlsHTML = `
<style>
#vpsm-graph-ctl{padding:12px 14px;border-top:1px solid rgba(255,255,255,.06);font:12px system-ui,-apple-system,sans-serif;color:#9aa4b2}
#vpsm-graph-ctl h4{margin:0 0 8px;font-size:11px;letter-spacing:.5px;text-transform:uppercase;color:#6b7280;font-weight:600}
#vpsm-graph-ctl label{display:flex;align-items:center;gap:7px;margin:6px 0;cursor:pointer}
#vpsm-graph-ctl .vpsm-row{display:flex;align-items:center;gap:8px;margin:9px 0}
#vpsm-graph-ctl .vpsm-row input[type=range]{flex:1}
#vpsm-graph-ctl button{width:100%;margin:5px 0;padding:6px;border:1px solid rgba(255,255,255,.12);background:#141a26;color:#cbd5e1;border-radius:5px;cursor:pointer;font:11px system-ui}
#vpsm-graph-ctl button:hover{background:#1b2433}
#vpsm-graph-ctl .vpsm-count{color:#6b7280;font-size:11px}
</style>
<script>
(function(){
  if (typeof nodesDS==='undefined' || typeof network==='undefined') return;
  var sb=document.getElementById('sidebar'); if(!sb) return;
  var maxDeg=0; nodesDS.forEach(function(n){ if((n._degree||0)>maxDeg) maxDeg=n._degree||0; });
  var el=document.createElement('div'); el.id='vpsm-graph-ctl';
  el.innerHTML=
    '<h4>Organização</h4>'+
    '<label><input type="checkbox" id="vg-tests"> Esconder testes</label>'+
    '<label><input type="checkbox" id="vg-leaves"> Esconder folhas (grau &le;1)</label>'+
    '<div class="vpsm-row"><span>Grau mín.</span><input type="range" id="vg-deg" min="0" max="'+maxDeg+'" value="0"><span class="vpsm-count" id="vg-degv">0</span></div>'+
    '<button id="vg-collapse">Colapsar por comunidade</button>'+
    '<button id="vg-freeze">Congelar layout</button>'+
    '<div class="vpsm-count" id="vg-stat"></div>';
  sb.appendChild(el);
  function isTest(f){ f=(f||'').toLowerCase(); return f.indexOf('_test')>=0||f.indexOf('.test.')>=0||f.indexOf('/tests/')>=0||f.indexOf('.spec.')>=0; }
  function apply(){
    var ht=document.getElementById('vg-tests').checked;
    var hl=document.getElementById('vg-leaves').checked;
    var md=parseInt(document.getElementById('vg-deg').value,10)||0;
    var hidden=0,total=0,upd=[];
    nodesDS.forEach(function(n){ total++;
      var d=n._degree||0;
      var h=(ht&&isTest(n._source_file))||(hl&&d<=1)||(d<md);
      upd.push({id:n.id,hidden:!!h}); if(h)hidden++; });
    nodesDS.update(upd);
    document.getElementById('vg-stat').textContent=(total-hidden)+' visíveis · '+hidden+' ocultos';
  }
  document.getElementById('vg-tests').onchange=apply;
  document.getElementById('vg-leaves').onchange=apply;
  var deg=document.getElementById('vg-deg');
  deg.oninput=function(){ document.getElementById('vg-degv').textContent=deg.value; apply(); };
  var collapsed=false;
  document.getElementById('vg-collapse').onclick=function(){
    if(!collapsed){
      var comms={}; nodesDS.forEach(function(n){ if(n._community!=null) comms[n._community]=n._community_name||('Comunidade '+n._community); });
      Object.keys(comms).forEach(function(c){
        try{ network.cluster({ joinCondition:function(o){ return String(o._community)===String(c); },
          clusterNodeProperties:{ label:comms[c], shape:'dot', size:26, color:'#3b82f6', font:{color:'#e5e7eb',size:14} } }); }catch(e){}
      });
      collapsed=true; this.textContent='Expandir comunidades';
    } else {
      try{ network.setData({nodes:nodesDS, edges:edgesDS}); }catch(e){}
      collapsed=false; this.textContent='Colapsar por comunidade';
    }
  };
  var frozen=false;
  document.getElementById('vg-freeze').onclick=function(){
    frozen=!frozen; try{ network.setOptions({physics:{enabled:!frozen}}); }catch(e){}
    this.textContent=frozen?'Descongelar layout':'Congelar layout';
  };
})();
</script>
`
