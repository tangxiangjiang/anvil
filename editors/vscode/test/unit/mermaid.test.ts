import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';

const ROOT = join(__dirname, '..', '..', '..');
// Normalised: a checkout with CRLF line endings must not change what the slices below find.
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8').replace(/\r\n/g, '\n');
const CHAT_CSS = readFileSync(join(ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8')) as {
  devDependencies: Record<string, string>;
  scripts: Record<string, string>;
};

const EXAMPLE = `flowchart TB
    subgraph AGC["AgcRHI (Agc 侧)"]
        RRes["FAgcReservedResource<br/>CommittedSize 成员"]
        VMB["FAgcVirtualMemoryBlock<br/>AgcBase.h:78-138<br/>BaseBlock 是 public"]
        CtxH["AgcContextCommon.h:525<br/>判 PlatformType == ReservedResource"]
        BaseCpp["AgcBase.cpp:82-85<br/>ContainsPtr 硬编码 4 个类型"]
        RHI["AgcRHI.cpp:816<br/>HugeGpuMemoryCollect()"]
    end

    subgraph CORE["Core (PS5PlatformMemory)"]
        Enum["EVirtualMemoryPlatformType<br/>Default/GPU/HugeGPU/ReservedResource/FrameBuffer"]
        Block["FPlatformVirtualMemoryBlock<br/>Commit / Decommit / FreeVirtual<br/>64KB 对齐"]
        PC["FPageCaches<br/>全局 PageCacheLock<br/>MemAvailable / MemDiscardable"]
        VM["VM_TYPES<br/>ReservedResource 128GiB aperture<br/>sceKernelSetPrtAperture"]
    end

    VMB -->|"① 包住"| Block
    VMB -->|"② 透传枚举"| Enum
    RRes -->|"③ AllocateVirtual"| VMB
    CtxH -->|"④ 反向判类型语义"| Enum
    BaseCpp -->|"⑤ 反向判类型语义"| Enum
    RHI -->|"⑥ 反向调堆整理"| VM
    Block --> PC
    PC --> VM

    style VMB fill:#ffe8cc
    style Enum fill:#ffd6d6
    style PC fill:#ffd6d6`;

test('Mermaid is vendored and loaded before the chat renderer', () => {
  assert.equal(PACKAGE.devDependencies.mermaid, '^12.2.0');
  assert.equal(PACKAGE.scripts['vendor:mermaid'], 'node scripts/vendor-mermaid.mjs');
  const mermaid = CHAT_PANEL.indexOf("asset('vendor/mermaid/mermaid.min.js')");
  const chat = CHAT_PANEL.indexOf("asset('chat.js')");
  assert.ok(mermaid > 0 && mermaid < chat);
});

test('a closed Mermaid fence becomes a sanitized inline SVG', () => {
  assert.match(CHAT_JS, /isMermaidBlock\(block\) && block\.closed === true/);
  assert.match(CHAT_JS, /securityLevel: 'strict'/);
  assert.match(CHAT_JS, /theme: light \? 'default' : 'dark'/);
  assert.match(CHAT_JS, /new DOMParser\(\)\.parseFromString\(source, 'text\/html'\)/);
  assert.match(CHAT_JS, /parsed\.querySelector\('svg'\)/);
  assert.match(CHAT_JS, /querySelectorAll\('script, iframe, object, embed, image'\)/);
  assert.match(CHAT_JS, /document\.importNode\(root, true\)/);
  assert.doesNotMatch(CHAT_JS, /themeVariables:/);
  assert.doesNotMatch(CHAT_JS, /htmlLabels: false/);
  assert.doesNotMatch(CHAT_JS, /\.innerHTML\s*=/);
  assert.match(CHAT_CSS, /\.md-mermaid-svg:not\(\.md-mermaid-large\)\s*\{[^}]*--anvil-mermaid-natural-width/s);
  assert.match(CHAT_CSS, /\.md-mermaid-svg:not\(\.md-mermaid-large\)\s*\{[^}]*max-width:\s*100% !important/s);
  assert.match(CHAT_CSS, /\.md-mermaid-svg:not\(\.md-mermaid-large\)\s*\{[^}]*max-height:\s*min\(560px, 62vh\)/s);
  assert.match(CHAT_CSS, /\.md-mermaid-large\s*\{[^}]*max-width:\s*none/s);
  assert.doesNotMatch(CHAT_CSS, /\.md-mermaid-svg\s*\{[^}]*width:\s*100%/s);
  assert.doesNotMatch(CHAT_CSS, /\.md-mermaid-svg\s*\{[^}]*min-width:\s*640px/s);
});

test('unlabelled Mermaid-shaped code is detected without stealing ordinary code', () => {
  const start = CHAT_JS.indexOf('function isMermaidBlock(');
  const detect = CHAT_JS.slice(start, CHAT_JS.indexOf('\n  }', start) + 4);
  assert.match(detect, /lang === 'mermaid'/);
  assert.match(detect, /lang !== '' && lang !== 'text' && lang !== 'plaintext'/);
  assert.match(detect, /flowchart\|graph\|sequenceDiagram\|classDiagram/);
  assert.match(detect, /String\(block\.text \|\| ''\)\.trimStart\(\)/);
});

test('the diagram uses Mermaid standard themes for the active VS Code appearance', () => {
  assert.match(CHAT_JS, /document\.body\.classList\.contains\('vscode-light'\)/);
  assert.match(CHAT_JS, /theme: light \? 'default' : 'dark'/);
  assert.doesNotMatch(CHAT_JS, /themeCSS:/);
  assert.match(CHAT_CSS, /\.md-mermaid-svg \.messageText,[\s\S]*?fill: #d0d0d0 !important/);
  assert.match(CHAT_CSS, /\.md-mermaid-svg \.actor,[\s\S]*?--vscode-editorWidget-background/);
  assert.match(CHAT_CSS, /\.md-mermaid-svg \.messageLine0,[\s\S]*?stroke: var\(--vscode-editor-foreground\) !important/);
  assert.match(CHAT_CSS, /\[id\$='-sequencenumber'\][\s\S]*?stroke: var\(--vscode-focusBorder\) !important/);
  assert.match(CHAT_CSS, /foreignObject \.actor \*[\s\S]*?-webkit-text-fill-color: #d0d0d0 !important/);
  assert.match(CHAT_JS, /normalizeMermaidContrast\(diagram\)/);
  const contrast = CHAT_JS.slice(CHAT_JS.indexOf('function normalizeMermaidContrast('));
  assert.match(contrast, /diagram\.querySelectorAll\('\*'\)/);
  assert.match(contrast, /diagram\.querySelectorAll\('text, tspan'\)/);
  assert.match(contrast, /setProperty\('-webkit-text-fill-color', foreground, 'important'\)/);
  assert.match(contrast, /setMermaidNaturalSize\(diagram\)/);
  assert.match(CHAT_JS, /--anvil-mermaid-natural-width/);
  assert.match(contrast, /querySelectorAll\(\s*'\.node rect, \.node polygon, \.node path, \.node circle, \.node ellipse'/);
  assert.match(contrast, /shape\.style\.setProperty\('fill', nodeFill, 'important'\)/);
  assert.match(contrast, /'#d0d0d0'/);
  assert.match(contrast, /setProperty\('fill', '#d8b7b2', 'important'\)/);
  assert.match(contrast, /querySelectorAll\('\[id\$="-sequencenumber" i\]'\)/);
  assert.match(contrast, /createElementNS\('http:\/\/www\.w3\.org\/2000\/svg', 'circle'\)/);
});

test('a diagram opens into a keyboard-accessible zoomable viewport', () => {
  assert.match(CHAT_JS, /view\.setAttribute\('aria-label', 'View Mermaid diagram in large view'\)/);
  assert.match(CHAT_JS, /view\.addEventListener\('click', async \(\) =>/);
  assert.match(CHAT_JS, /openMermaidDiagram\(diagram, source\)/);
  assert.doesNotMatch(CHAT_JS, /diagram\.addEventListener\('click'/);
  assert.match(CHAT_JS, /openMermaidDiagram\(diagram, source\)/);
  const open = CHAT_JS.slice(CHAT_JS.indexOf('function openMermaidDiagram('));
  assert.match(open, /el\('dialog', 'md-mermaid-dialog'\)/);
  assert.match(open, /let zoom = 1\.5/);
  assert.match(open, /zoom = Math\.max\(0\.5, zoom - 0\.25\)/);
  assert.match(open, /zoom = Math\.min\(3, zoom \+ 0\.25\)/);
  assert.match(open, /pan\.setAttribute\('aria-pressed', panEnabled \? 'true' : 'false'\)/);
  assert.match(open, /viewport\.setPointerCapture\(event\.pointerId\)/);
  assert.match(open, /viewport\.scrollLeft = drag\.left/);
  assert.match(open, /mermaidToolbarButton\('Close diagram', 'Close diagram'/);
  assert.match(open, /el\('div', 'md-mermaid-toolbar-controls'\)/);
  assert.doesNotMatch(open, /el\('button', 'ghost', 'Close'\)/);
  assert.match(open, /dialog\.showModal\(\)/);
  assert.match(open, /dialog\.addEventListener\('close'/);
  assert.match(CHAT_CSS, /\.md-mermaid-viewport\s*\{[^}]*overflow:\s*auto/s);
  assert.match(CHAT_CSS, /\.md-mermaid-viewport\.panning\s*\{[^}]*cursor:\s*grab/s);
  assert.match(CHAT_CSS, /\.md-mermaid-toolbar\s*\{[^}]*user-select:\s*none/s);
  assert.match(CHAT_CSS, /\.md-mermaid-toolbar button\s*\{[^}]*user-select:\s*none/s);
  assert.match(CHAT_CSS, /\.md-mermaid-toolbar-controls\s*\{[^}]*border-radius:\s*7px/s);
  assert.match(
    CHAT_CSS,
    /\.md-mermaid-toolbar-controls button\.md-mermaid-toolbar-button\s*\{[^}]*display:\s*grid[^}]*place-items:\s*center[^}]*padding:\s*0/s,
  );
  assert.match(CHAT_CSS, /\.md-mermaid-toolbar-button\.close::before\s*\{[^}]*position:\s*absolute/s);
  assert.match(CHAT_JS, /const wrap = el\('div', 'md-mermaid-wrap'\)/);
  assert.match(CHAT_JS, /figure\.replaceChildren\(surface\);\s*wrap\.appendChild\(view\)/);
  assert.match(CHAT_CSS, /\.md-mermaid-view\s*\{[^}]*top:\s*-29px[^}]*opacity:\s*0\.64/s);
  assert.match(CHAT_CSS, /background:\s*var\(--vscode-editorWidget-background/);
  assert.doesNotMatch(CHAT_CSS, /\.md-mermaid-view\s*\{[^}]*--vscode-button-background/s);
  assert.match(CHAT_CSS, /\.md-mermaid-view:focus-visible/);
});

test('the reported flowchart exercises labels, edges, subgraphs and styles', () => {
  assert.match(EXAMPLE, /subgraph AGC/);
  assert.match(EXAMPLE, /<br\/>/);
  assert.match(EXAMPLE, /-->\|"① 包住"\|/);
  assert.match(EXAMPLE, /style VMB fill:#ffe8cc/);
  assert.match(CHAT_JS, /renderer\.render\(id, previewSource\)/);
  assert.match(CHAT_JS, /wrappingWidth: 260/);
  assert.match(CHAT_JS, /nodeSpacing: 48/);
  assert.match(CHAT_CSS, /\.md-mermaid-svg \.flowchart-link\s*\{[^}]*fill: none !important/s);
  assert.match(CHAT_CSS, /\.md-mermaid-svg \.node rect\s*\{[^}]*rx: 6px[^}]*ry: 6px/s);
  assert.match(CHAT_CSS, /\.md-mermaid-svg \.cluster rect\s*\{[^}]*rx: 8px[^}]*ry: 8px/s);
  assert.match(CHAT_CSS, /\.md-mermaid-svg \.edgeLabel p\s*\{[^}]*padding: 2px 5px/s);
  assert.match(CHAT_CSS, /\.md-mermaid-svg foreignObject \*[\s\S]*?line-height: 1\.15 !important/);
});

test('class diagrams use a compact inline source and retain full detail for View', () => {
  assert.match(CHAT_JS, /function compactClassDiagramSource\(source\)/);
  assert.match(CHAT_JS, /kept\[kind\] < 3/);
  assert.match(CHAT_JS, /kind === 'method' \? '\+\.\.\.\(\)' : '\.\.\.'/);
  assert.match(CHAT_JS, /const previewSource = compactClassDiagramSource\(source\)/);
  assert.match(CHAT_JS, /renderer\.render\(id, previewSource\)/);
  assert.match(CHAT_JS, /renderer\.render\(id \+ '-detail', source\)/);
  assert.match(CHAT_JS, /openMermaidDiagram\(await detailPromise, source\)/);

  const start = CHAT_JS.indexOf('function classMemberKind(');
  const end = CHAT_JS.indexOf('\n  /**\n   * Draws a closed Mermaid fence.', start);
  const context: { compact?: (source: string) => string } = {};
  runInNewContext(`${CHAT_JS.slice(start, end)}\ncompact = compactClassDiagramSource;`, context);
  const compact = context.compact?.(`classDiagram
class Example {
  +one: int
  +two: int
  +three: int
  +four: int
  +one()
  +two()
  +three()
  +four()
  +five()
}`);
  assert.ok(compact);
  assert.match(compact, /\+three: int/);
  assert.doesNotMatch(compact, /\+four: int/);
  assert.match(compact, /\n {2}\.\.\.\n/);
  assert.match(compact, /\+three\(\)/);
  assert.doesNotMatch(compact, /\+four\(\)/);
  assert.match(compact, /\+\.\.\.\(\)/);
});

test('relationship lines and markers are normalized after Mermaid inline styles', () => {
  assert.match(CHAT_JS, /\.relationshipLine, \.relation, \.edgePath path, path\.flowchart-link/);
  assert.match(CHAT_JS, /path\.style\.setProperty\('fill', 'none', 'important'\)/);
  assert.match(CHAT_JS, /marker path, marker polygon, marker circle/);
  assert.match(CHAT_JS, /marker\.style\.setProperty\('fill', relationColor, 'important'\)/);
});

test('code and diagram previews are height-bounded with quiet scrollbars', () => {
  assert.match(CHAT_CSS, /\.md-code\s*\{[^}]*max-height:\s*min\(440px, 55vh\)/s);
  assert.match(CHAT_CSS, /\.md-mermaid\s*\{[^}]*max-height:\s*min\(600px, 68vh\)/s);
  assert.match(CHAT_CSS, /\.md-code::-webkit-scrollbar,[\s\S]*?width:\s*0\s*!important/);
  assert.match(CHAT_CSS, /\.md-code\.scroll-active::-webkit-scrollbar,[\s\S]*?width:\s*10px\s*!important/);
  assert.match(CHAT_CSS, /\.md-code::-webkit-scrollbar-thumb,[\s\S]*?background-color:\s*transparent\s*!important/);
  assert.match(CHAT_CSS, /\.md-code\.scroll-active::-webkit-scrollbar-thumb,[\s\S]*?--vscode-scrollbarSlider-background/);
  assert.match(CHAT_CSS, /\.md-code::-webkit-scrollbar-button,[\s\S]*?display:\s*none\s*!important/);
  assert.doesNotMatch(CHAT_CSS, /\.md-(?:code|mermaid):focus-within::-webkit-scrollbar-thumb/);
  assert.match(CHAT_CSS, /\.md-code:focus-visible,[\s\S]*?--vscode-focusBorder/);
  assert.match(CHAT_JS, /function autoHideScrollbar\(node\)/);
  assert.match(CHAT_JS, /setTimeout\(hide, 850\)/);
  assert.match(CHAT_JS, /node\.tabIndex = 0/);
  assert.match(CHAT_JS, /figure\.tabIndex = 0/);
});
