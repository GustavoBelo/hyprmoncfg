# Modo Console (Couch Mode) — estado da sessão e o que falta

Branch: `main` do fork `GustavoBelo/hyprmoncfg` · commits `669e5bd`, `a4e9093`,
`880e650` · plugin em `GustavoBelo/omarchy-hyprmoncfg` (`88b0a57`).
Roteiro do que falta: **[TV-TEST.md](TV-TEST.md)**.

---

## 1. De onde partimos

Existia uma tentativa de portar o engine bash do OpenCouch para Go. **Compilava e os
testes passavam, mas não funcionava**: o log do usuário mostra 4 sessões de `play` e
4 × `Big Picture window not detected`, além de um `restore` disparando no meio de um
`play`.

O trabalho desta sessão foi: descobrir por quê, consertar, e ir além do OpenCouch.

---

## 2. Decisões tomadas

| Assunto | Decisão |
|---|---|
| Base | Feature dentro do hyprmoncfg (não projeto separado) |
| Dono da sessão | Daemon `hyprmoncfgd`, via IPC |
| Layout da TV | Perfil dedicado **gerado**, com edição limitada a 5 campos |
| Gerenciamento | Exige `hyprmoncfg manage` |
| Escopo | Sessão de console completa + gamescope |
| Gatilhos | Manual, controle ligado, Big Picture aberto por fora |
| Publicação | Fork agora, upstream depois |
| Lista de apps | Seleção, nunca digitação |

---

## 3. Descobertas — o conhecimento caro desta sessão

Tudo abaixo foi verificado na máquina, não deduzido.

### 3.1 Por que o Big Picture nunca era detectado

Não era timing. É uma regra estática do Omarchy:

```lua
-- /usr/share/omarchy/default/hypr/apps/steam.lua
o.window("steam", { float = true })
o.window({ class = "steam", title = "Steam" }, { center = true, size = { 1100, 700 } })
```

O Steam é forçado a flutuante 1100x700. Os três tells do detector falhavam juntos:
o título ainda era "Steam"; o endereço já era conhecido (`steam://open/bigpicture`
reusa a janela); e 1100x700 não cobre monitor. **O título mudando para "Steam Big
Picture Mode" é o único tell que sobrevive** — e é exatamente o que o usuário já
contornava à mão no `hyprland.lua`.

### 3.2 Nenhuma opção de config muda em runtime no Hyprland 0.56

```
$ hyprctl keyword general:allow_tearing false
keyword can't work with non-legacy parsers. Use eval.
$ echo $?
0
```

E a alternativa Lua também é aceita e ignorada:

```
hl.config("general:allow_tearing", true)   → pcall ok, valor não muda
```

Ambos são **no-ops silenciosos**. Nenhum hook tenta mexer em opção; o `doctor`
avisa para o usuário setar `general:allow_tearing` na config dele.

**Correção posterior:** a primeira versão desta nota dizia que `hyprctl dispatch`
funcionava. Não funciona — ver 3.9. O que de fato funciona em runtime é
`hl.window_rule()` (devolve handle), `hl.get_config()` para leitura, e os
dispatchers **só na forma Lua**.

### 3.3 Nomes de evento do Hyprland

`strings /usr/bin/Hyprland` → `openwindow`, `closewindow`, `activewindow`,
`windowtitle`, `windowtitlev2`, `configreloaded`, `monitoradded(v2)`,
`monitorremoved(v2)`. **Não existem** `createwindow` nem `destroywindow`, que era o
que o `couch watch` antigo assinava.

### 3.4 O bug do `cm: auto` — causa de um layout trocado ao vivo

Ao ligar o gerenciamento, o daemon acendeu a TV e espelhou a 1080p120. Causa:

```
cm  perfil="auto"  ao vivo="srgb"  →  same=false
```

`auto` é um *pedido* de preset de cor; o Hyprland resolve e reporta `srgb`. Como a
comparação era de string, **um perfil salvo com `cm: auto` nunca reconhecia o próprio
estado aplicado** — e é isso que o hyprmoncfg grava para qualquer display SDR. Com o
`ExactStateMatch` sempre falhando, o `BestMatch` impunha outro perfil sobre uma mesa
que não precisava mudar. Corrigido em `internal/profile/match.go` (`colorPresetsAgree`).

### 3.5 O `unmanage` estava sendo desfeito em silêncio

`Unmanage` remove o include (`daemon/ipc.go:97`), mas `Engine.Apply` chamava
`ensureConfigInclude` incondicionalmente (`apply/apply.go:217`). Todo `couch play`
recolocava o include, e por isso a escala ao vivo era 1 e não o 1.25 do
`monitors.lua`.

### 3.6 Heurística de modo — três armadilhas

Na TV Samsung do host:

| Regra ingênua | Escolhe | Problema |
|---|---|---|
| Maior modo | 4096x2160@120 | 17:9 cinema, letterboxa 16:9 |
| Maior refresh | 1920x1080@144 | Larga 4K para ganhar 24Hz |
| Nativo puro | 3840x2160@60 | Perde 120Hz |

Regra final: **maior resolução entre os modos ≥100Hz, na proporção nativa** →
`3840x2160@120`. Fallback para maior resolução quando nada passa de 100Hz.

### 3.7 `/dev/input/js*` não é fonte confiável

Vazio no host apesar de `joydev` carregado. A detecção passou a ler
`/sys/class/input/event*/device/capabilities/key` e testar o bit `BTN_SOUTH` (0x130).

### 3.9 `hyprctl dispatch` também é recusado no parser Lua

```
$ hyprctl dispatch focuswindow address:0x55d99b7888e0
error: [string "return hl.dispatch(focuswindow address:0x55d9…)"]:1: ')' expected
```

Ele embrulha o pedido como `hl.dispatch(<texto cru>)`, que não é Lua válido —
**toda** forma clássica é recusada. O `WakeDisplays` do hyprmoncfg já sabia disso
e escolhia o dialeto à mão; o código do couch não. As formas Lua verificadas por
efeito, numa janela descartável:

```lua
hl.dispatch(hl.dsp.window.close({ window = w }))
hl.dispatch(hl.dsp.window.float({ action = "unset", window = w }))
hl.dispatch(hl.dsp.window.fullscreen({ mode = "fullscreen", action = "set", window = w }))
```

Não existe busca por endereço: varre-se `hl.get_windows()` casando `tostring(w)`
com `HL.Window(0x…)`. E `hl.dsp.window.move` é mais um no-op silencioso — aceita
e não move.

### 3.8 Casamento de app por substring de título é perigoso

Log de 2026-08-24: `apps: closed tracked processes [32 PIDs]`. Vinha de
`strings.Contains(class+" "+title, target)`. Agora é exato, sobre classe ou comm.

---

## 4. O que foi construído

### Núcleo (`internal/couch/`)
- **`profile.go`** — geração do perfil console, heurística de TV/modo, validação e
  invariantes travados por construção: TV sempre ligada, `transform 0`, `scale 1`,
  posição derivada, modo só da lista real, ao menos uma saída ligada.
- **`steam.go`** — detecção com níveis de confiança (`Likely` / `Certain`). Só
  `Certain` dispara entrada automática, senão abrir a biblioteca do Steam arrastaria
  o usuário para a TV. Fim da espera de 90 s (`ResolveSteamPID`).
- **`window.go`** — `KeepBigPictureFullscreen` (tila antes de fullscreen, senão a
  janela volta à geometria flutuante), parsing de payload de evento.
- **`controllers.go`** — evdev `BTN_GAMEPAD` com fallback para `js*`.
- **`apps.go`** — casamento exato; parse de `Exec` que pula `env`/`VAR=`; lista de
  wrappers (`flatpak`, `xdg-terminal-exec`, `omarchy-launch-webapp`…) descartados
  como alvo.
- **`candidates.go`** — candidatos para o seletor: janelas abertas primeiro (classe
  exata garantida), depois apps instalados.
- **`gamescope.go`** — sessão aninhada, dimensões vindas do perfil console.
- **`hooks/`** — áudio HDMI, idle, DND, barra, night light. Cada um registra o que
  mudou e restaura exatamente isso; nunca um toggle cego.

### Daemon (`internal/daemon/couch.go`)
Máquina de estados `Idle → Entering → Playing → Leaving`. Sessão sobrevive à TUI,
reconcilia depois de `kill -9` (snapshot da mesa dentro do `session.json`). IPC
`couch.status/start/stop`. Uma única conexão socket2, despachada por tipo — duas
assinaturas causavam dependência de ordem de conexão.

### Superfícies
- **TUI** — editor limitado (TV, modo, HDR só se o EDID suportar, VRR, mesa),
  gatilhos, hooks, gamescope; **seletor múltiplo de apps** substituindo o campo de
  texto. Modelo de linhas dinâmico com `couchRowIndex`.
- **CLI** — `couch doctor` no lugar do `check` decorativo; `apps suggest/add/remove`;
  `--json` consertado.
- **Painel Omarchy** — estado empurrado pelo `appstatus`, ações por IPC. Removidos o
  processo em spawn e o timer de 2 s × 5.

---

## 5. Estado verificado

Build, vet, gofmt e testes limpos. Além disso, **exercitado contra o compositor
ao vivo** em 2026-08-30:

| O que | Resultado |
|---|---|
| Big Picture aparece | 6–7 s (era 150 s, ou nunca) |
| Steam já aberto | resolvido no mesmo segundo — o caminho que falhava 4/4 |
| Fullscreen na TV | `fs=2 size=1920x1080 mon=1`, mantido por 22 s |
| Gatilho de Big Picture externo | entrou sozinho |
| `FocusMonitor` | `HDMI-A-1 focused:true` |
| Fechar apps | `closed 1 window(s) from the close list` |
| Daemon ignora troca de foco | 21 eventos emitidos, 0 ações |
| `kill -9` no meio da sessão | layout, barra, DND e `session.json` recuperados |
| Parada normal | layout, áudio, barra e idle exatos |
| Perfil gerado, HDR por EDID, seletor de apps | contra o hardware real |

**Instalado:** binários em `~/.local/bin`, serviço `hyprmoncfgd` habilitado,
plugin copiado e habilitado, `.desktop` + ícone em `~/.local/share` com o
caminho absoluto do binário.

### Seis bugs que só a execução real revelou

Todos corrigidos em `a4e9093` e `880e650`.

1. **Nenhuma ação de janela funcionava** — `hyprctl dispatch` vira
   `hl.dispatch(<texto cru>)` no parser Lua, que não é Lua válido.
2. **Hook da barra invertido** — escondia ao sair, não durante.
3. **Log ao contrário** — dizia "no running process matched" enquanto fechava.
4. **DND nunca funcionou** — `omarchy-shell notifications dnd` não é método.
   O par certo é `dndState`/`setDnd`, que ainda é idempotente.
5. **Fullscreen não grudava** — o Steam sai dele sem mudar o título.
6. **Undo dos hooks não sobrevivia a crash** — eram closures; agora são dados
   no `session.json`, ao lado do snapshot do layout.

O padrão que atravessa quase todos: **aceita o comando, sai com 0, não faz
nada**. Verifique sempre por efeito, nunca por código de retorno.

## 6. O que falta

### 6.1 Ação do usuário (bloqueia o resto)

```sh
sudo rm -f /usr/local/bin/hyprmoncfg /usr/local/bin/hyprmoncfgd
```

Os binários de 26/ago têm precedência no PATH e **corrompem a configuração**: a TUI
antiga não conhece o campo `layout`, então grava de volta o esquema velho e perde o
layout e o `apps_to_close`. Também reescreve o perfil `couch` **sem** `managed_by`,
o que o torna candidato do casamento automático. Aconteceu uma vez nesta sessão;
volta a acontecer enquanto os dois existirem.

### 6.2 Decisão pendente: o layout

Hoje **1920x1080@120, HDR, VRR off, mesa ligada** — herdado por migração do perfil
`game`, que era o contorno de espelhamento. A heurística proporia
**3840x2160@120, HDR, VRR, mesa desligada** (4K nativo a 120Hz, confirmado na lista
de modos da TV).

### 6.3 Código

1. **Regerar o perfil a cada edição na TUI.** Hoje o perfil só é regerado no `couch
   enable` e no início de cada sessão. O layout aplicado está sempre correto, mas o
   arquivo pode ficar velho entre uma edição e a próxima sessão. Fechar a folga
   também faria um erro de geração aparecer na hora da edição.
2. **Separar o commit do `ExactStateMatch`** (+ os `ForcedProfile: "Home"` nos testes
   do daemon) e o do `colorPresetsAgree`. São correções do núcleo de perfis, não do
   modo console, e valem PR upstream por si só.
3. **`minimumVersion` do plugin** está em `1.15.0` enquanto o build reporta `dev`.
   Passa hoje porque `versionAtLeast` trata `dev` como compatível, mas precisa subir
   junto com a primeira release do fork.
4. **Bump de versão e release** do hyprmoncfg e do plugin (`1.3.0` já no manifest).

### 6.4 O que ainda depende da TV acesa

Ver **[TV-TEST.md](TV-TEST.md)** — o roteiro completo. Em resumo: o áudio indo
para o HDMI (em standby a TV não apresenta ELD, então o sink não existe), o
resultado visual, a decisão entre 1920x1080@120 e 3840x2160@120, voltar de um
jogo, o gatilho pelo controle, e o gamescope (não instalado).

### 6.5 Higiene

Resolvido: os binários antigos em `/usr/local/bin` foram removidos, e os dois
gatilhos automáticos estão ligados na config.

Pendente: dois window rules inertes ficaram no Hyprland de sondagem
(`^hyprmoncfg-probe-nonexistent$`, `^hyprmoncfg-probe2$`). Nenhuma janela casa
com eles; qualquer `hyprctl reload` limpa.

## 7. Ordem sugerida

1. **[TV-TEST.md](TV-TEST.md)**, em casa com a TV acesa. Os testes 1–3 são os
   que decidem se isto vira um console de verdade.
2. Fixar o layout escolhido (provavelmente 3840x2160@120 + VRR).
3. Fechar a folga do perfil (6.3.1): regerar a cada edição na TUI.
4. Separar os commits do núcleo de perfis (6.3.2) para PR upstream.
5. gamescope, se quiser.
