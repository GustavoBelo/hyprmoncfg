# Modo Console (Couch Mode) — estado da sessão e o que falta

Branch: `main` do fork `GustavoBelo/hyprmoncfg`, com o upstream `v1.16.1`
mesclado · plugin em `GustavoBelo/omarchy-hyprmoncfg` (`b2d9e01`).
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

> Esta tabela é da sessão de 29–30/08 pela manhã, com a TV em standby e todas
> as sessões abertas por `couch play` num terminal. É verdadeira e foi
> insuficiente: ver "Mais seis, com a TV acesa" logo abaixo.

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

### Mais seis, com a TV acesa (2026-08-30, tarde)

Com a TV ligada de verdade, e exercitando os caminhos automáticos em vez do
`couch play` do terminal, apareceram outros seis. Corrigidos em `df4c712`,
`e602bd4` e `924a45c`.

7. **O som ia para o monitor da mesa.** Uma placa apresenta um pin de áudio HDMI
   por conector, e todo sink derivado deles se descreve como a *placa*
   (`Navi 48 HDMI/DP Audio Controller`), nunca como o display. O desempate por
   descrição não casava com nada e o fallback pegava o primeiro sink "hdmi" — o
   da mesa. Pior: a placa expõe **um sink HDMI por vez**, então acender a TV não
   cria um segundo; é preciso trocar o **perfil do card**. O elo que funciona é o
   EDID: o ELD em `/proc/asound/card*/eld#*.*` traz o `monitor_name`, que vem do
   mesmo EDID que o Hyprland reporta. Aqui, `25G64` no pin 0 e `SAMSUNG` no pin 1.
8. **Todo gatilho automático estava quebrado.** Uma unit de usuário do systemd
   herda `XDG_RUNTIME_DIR` e um PATH pelado: sem `WAYLAND_DISPLAY`, sem
   `DISPLAY`, sem `HYPRLAND_INSTANCE_SIGNATURE`, sem `/usr/share/omarchy/bin`.
   O daemon fala com o Hyprland assim mesmo (o hyprctl aceita `--instance`
   descoberto), então nada parecia errado — mas o Steam que ele lançava morria
   com `Unable to open X11 display`, e os hooks de idle/DND/night light se
   declaravam indisponíveis em silêncio. Só o `couch play` do terminal escapava,
   por herdar o ambiente do shell. **É por isso que tudo parecia funcionar.**
9. **O gatilho do controle seguia a presença, não a conexão.** Perguntar "tem um
   controle plugado?" a cada 2 s fazia a sessão renascer 1 s depois de qualquer
   `couch stop` — não havia como sair do modo console sem desplugar o controle.
   A mesma leitura entrava em modo console no login de quem deixa o controle
   ligado. Agora dispara na borda 0→N, com a contagem semeada no arranque.
10. **Desligar o daemon abandonava a sessão.** Um `systemctl --user restart`
    deixava o layout da TV aplicado, o som na TV e a barra escondida, porque os
    hooks são desfeitos pelo processo que os aplicou. O `Reconcile` não cobre o
    restart: o registro ainda nomeia um processo que está só terminando, então o
    daemon novo o lê como vivo, descarta o registro, e deixa o casamento
    automático impor um perfil sobre um desktop que ninguém restaurou — foi
    exatamente o que aconteceu, aplicou o `game`. E não bastou esperar: o
    `KillMode` padrão sinaliza o cgroup inteiro, então o `hyprctl` e o `pactl`
    do restore morriam no meio (`signal: terminated`). `KillMode=mixed`.
11. **O gamescope não morria com a sessão.** Primeira execução do código, que
    nunca tinha rodado. Parar a sessão devolvia layout, áudio e barra e deixava
    o gamescope no ar: uma janela fullscreen sem nada atrás, que o Hyprland
    mudava para a mesa assim que a TV era desligada. Mais um zumbi por sessão,
    porque o daemon nunca esperava pelos filhos que criava.
12. **O `.goreleaser.yml` publicava no repo do upstream.** Uma tag no fork
    tentaria cortar release em `crmne/hyprmoncfg`.

O padrão novo é outro, e vale registrar: **o que funciona quando você testa à
mão pode estar quebrado quando o programa se testa sozinho.** Todo teste da
sessão anterior passou por um terminal, e o terminal traz junto um ambiente que
o daemon não tem. Exercite o caminho automático, não o caminho conveniente.

## 6. Onde isto ficou

### 6.1 Resolvido

| | |
|---|---|
| Layout da TV | **3840x2160@120, HDR** — escolhido olhando, contra 1080p120. Fixado no `couch.json`. |
| Áudio | vai para o pin da TV e volta. Medido pela porta (`hdmi-output-1`), não pelo nome. |
| Gatilhos automáticos | funcionam: o daemon adota o ambiente da sessão gráfica. |
| Merge do upstream | `v1.16.1` integrado; o editor novo não expõe o perfil gerado. |
| PR upstream | [crmne/hyprmoncfg#51](https://github.com/crmne/hyprmoncfg/pull/51), draft, só o `colorPresetsAgree`. |
| Perfil na TUI | regerado a cada edição. |
| gamescope | instalado e exercitado; a linha sai do próprio perfil e morre com a sessão. |
| Versão | plugin em `1.4.0`, `minimumVersion` `1.17.0`; goreleaser aponta para o fork. |

### 6.2 O que falta

1. **Testes 4 e 5 do [TV-TEST.md](TV-TEST.md)** — voltar de um jogo, e o ciclo
   completo do controle (entrar ao plugar, sair após 60 s de uso). São os dois
   que precisam de alguém jogando.
2. **Cortar a tag `v1.17.0-rc.1`** e deixar o goreleaser publicar a pré-release.
   Tudo está preparado; falta só a tag.
3. **VRR continua desligado.** A heurística proporia ligado. Não foi testado com
   jogo rodando, que é o único lugar onde se vê diferença.
4. **`general:allow_tearing` está off.** O `doctor` avisa; é config manual do
   Hyprland, e nenhuma opção muda em runtime no parser Lua.

### 6.3 Coisas que ficaram sabidas e não consertadas

- **Um hook que falha ao restaurar prende o desktop para sempre.** O `Capture`
  devolve `nil` quando o estado já é o que a sessão quer — desenhado para não
  desfazer o que o usuário escolheu —, então uma sessão seguinte lê a barra
  escondida como intencional e nunca a devolve. Aconteceu aqui: o teste do
  `KillMode`, antes do conserto, deixou a barra e o DND presos, e as sessões
  seguintes não os tocaram. Com o desligamento consertado e o `Reconcile`
  cobrindo o crash, o caminho ficou estreito; distinguir "o usuário escolheu" de
  "uma sessão anterior largou" exigiria mais estado.
- **Sob gamescope o Big Picture não é detectado** — a classe da janela é
  `gamescope`. A sessão cai em vigiar o processo do Steam, que é o comportamento
  certo: sob gamescope existe uma janela só, e o título dela vira o do jogo, então
  casar por título leria "abriu um jogo" como "fechou o Big Picture".
- **`currentFormat` reporta `XRGB8888`** (8 bits) mesmo com `cm: hdr` aplicado, a
  1080p120 e a 4K120. As cores foram julgadas boas a olho. Não investigado se o
  HDR está de fato negociado ou se o Hyprland só reporta o formato do plano.

### 6.4 Higiene

Pendente: dois window rules inertes de sondagem no Hyprland
(`^hyprmoncfg-probe-nonexistent$`, `^hyprmoncfg-probe2$`). Nenhuma janela casa
com eles; qualquer `hyprctl reload` limpa.

## 7. Ordem sugerida

1. Testes 4 e 5 do [TV-TEST.md](TV-TEST.md), jogando de verdade.
2. Tag `v1.17.0-rc.1` e a pré-release.
3. Decidir o VRR com um jogo rodando.

## 8. Spike do gamescope-session (2026-08-31)

Decisão de rumo: aposentar o modo dentro do Hyprland e entregar a sessão ao
`gamescope-session` da comunidade. Antes de escrever código, um spike na
máquina real. Sete medições; três confirmaram o desenho, quatro o derrubaram.

Ambiente: Omarchy 4.0.0.alpha, Hyprland 0.56.2 sob uwsm, SDDM com autologin,
`gamescope-session-cachyos` 1.1.6, AMD, TV Samsung em HDMI-A-1 e monitor TCL
25G64 em DP-1.

### 8.1 O que funcionou

**O conector é uma variável de ambiente.** `/usr/lib/steamos/gamescope-session`
termina em `-O "${OUTPUT_CONNECTOR:-*,eDP-1}"`. Um drop-in em
`~/.config/systemd/user/gamescope-session.service.d/` com
`Environment=OUTPUT_CONNECTOR=HDMI-A-1` bastou:

```
drm: Connectors:  HDMI-A-2 (disconnected) / HDMI-A-1 (connected) / DP-1 (connected)
drm: selecting connector HDMI-A-1
```

O DP-1 ficou `enabled=disabled dpms=Off` — o gamescope conduz uma saída só. Os
nomes DRM batem exatamente com os do Hyprland, e o `ConsoleLayout.TVName` já
guarda `HDMI-A-1`, então o valor já existe na config.

**O botão "Switch to Desktop" resolve pelo PATH.** Um shim em `~/.local/bin`
foi executado no lugar do `/usr/bin/steamos-session-select` do pacote:

```
argv:     /home/belo/.local/bin/steamos-session-select plasma
ppid:     steam -srt-logger-opened -steamos3 -steampal -steamdeck -gamepadui
resolved: /home/belo/.local/bin/steamos-session-select
```

O argumento é `plasma`, não `desktop`. Como a resolução é por PATH, dá para
interceptar sem sobrescrever arquivo de pacote — requisito de projeto público.
O PATH da sessão vem do mesmo drop-in: o script faz `env >
$XDG_RUNTIME_DIR/gamescope-environment` e o `steam-launcher.service` lê isso
via `EnvironmentFile`.

**O painel por jogo do Steam existe porque a sessão o declara.** As ~30
variáveis `STEAM_GAMESCOPE_*_SUPPORTED` no topo do script são o que faz o Big
Picture oferecer HDR, FSR, tearing e limitador. O gamescope aninhado não
definia nenhuma — era esse o argumento para aposentá-lo, e ele se sustenta.

### 8.2 O que derrubou o desenho

**O modo não é nosso.** O gamescope escolheu `3840x2160@60Hz` — o preferido do
EDID — e não os 4K120 fixados no `couch.json`. Refresh se troca pelo Steam, que
grava em `GAMESCOPE_MODE_SAVE_FILE`. Consequência: o `ConsoleLayout` só precisa
do conector; modo, HDR e VRR saem da nossa config.

**Trocar de sessão pelo gerenciador de login é um beco sem saída.** O autologin
do SDDM só dispara quando o *SDDM* inicia. Ao fim de uma sessão vem o greeter:

```
17:12:31  Auth: sddm-helper exited successfully     (sessão gamescope terminou)
17:12:31  Greeter session started successfully      (greeter, não autologin)
17:14:11  Session "gamescope-session.desktop" selected ... for VT 2
```

Com `RememberLastSession=true` o greeter vem com Gamescope pré-selecionado, pede
senha e reentra no console. O usuário fica preso em loop. Reapontar o autologin
via `pkexec steam-set-session` — que funciona, passwordless, e aceita qualquer
nome de sessão — é irrelevante para a volta. E `steam-set-session` só sabe
escrever para SDDM e plasmalogin, então nada disso serve para greetd, ly ou
login por tty.

**A troca suja o gerenciador systemd do usuário, e ninguém limpa na saída.**
Quando a sessão gamescope morre de forma abrupta ela deixa para trás:

- `graphical-session-pre.target` **ativo** → o uwsm recusa o Hyprland com
  `A compositor or graphical-session* target is already active!` e a sessão
  seguinte morre no mesmo segundo (`Session started true` → `[PAM] Closing
  session` → `exited with 1`);
- unidades em `failed`: `gamescope-session`, `gamescope-mangoapp`,
  `gamescope-xbindkeys`, `ibus-gamescope`, `steam-notif-daemon`
  (`steamos-manager` nem existe fora do Deck);
- ambiente do gerenciador poluído: `XDG_CURRENT_DESKTOP=gamescope`,
  `DESKTOP_SESSION=gamescope-session`, `XDG_DESKTOP_PORTAL_DIR` vazio,
  `XDG_VTNR=2`, e `PATH` amputado para `/usr/local/bin:/usr/bin`.

O `start-gamescope-session` limpa isso na **entrada** (`stop
gamescope-session.target`, `stop graphical-session-pre.target`, `reset-failed`)
justamente porque sabe do problema — mas não há equivalente na saída.
Recuperação: parar os targets, `reset-failed`, `unset-environment` das XDG_*, e
reiniciar o gerenciador de login.

**Bluetooth.** O adaptador ficou `Powered: no` na sessão gamescope, sem controle
possível. Medido depois: ele está `Powered: no` no Omarchy também — não é
regressão da troca, é que nada liga o adaptador automaticamente nesta máquina.
Mas o modo console não tem painel de Bluetooth, então precisa ligá-lo sozinho.

**Inconclusivo — áudio.** O som foi parar na TV (`output:hdmi-stereo-extra1`,
`port=hdmi-output-1`, o pin do SAMSUNG) sem o nosso hook rodar. Não dá para
separar "o wireplumber escolheu" de "o wireplumber restaurou a escolha que nosso
hook gravou ontem", porque ele persiste isso entre boots. Precisa de outra
medição partindo de um padrão diferente.

### 8.3 Consequência para o desenho

O gerenciador de login não pode ser o mecanismo de troca. O que resta, e é mais
portátil do que a rota original, é **uma sessão que hospeda os dois
compositores**: o login manager (ou o tty) inicia um wrapper nosso, que roda o
compositor do usuário ou o `start-gamescope-session`, e troca quando o de dentro
sai. O login manager nunca vê a sessão terminar — logo, sem greeter, sem senha,
sem `RememberLastSession`, sem autologin, sem polkit, sem root em runtime. E a
limpeza que hoje ninguém faz passa a ter um lugar único por onde toda transição
obrigatoriamente passa.

### 8.4 O wrapper, medido

Protótipo em shell: o login manager inicia um wrapper; ele roda o compositor do
usuário ou o `start-gamescope-session`, e troca quando o de dentro sai. O pedido
de troca é um arquivo em `$XDG_RUNTIME_DIR` com `console` ou `desktop`; nenhum
arquivo significa logout de verdade, e o laço termina.

```
17:32:56  wrapper start mode=desktop vt=1 session=11
17:33:17  desktop saiu rc=0 (21s)   -> switching to: desktop
17:33:47  desktop saiu rc=0 (30s)   -> switching to: console
17:35:31  gamescope saiu rc=0 (104s)-> switching to: desktop
```

Mesma sessão do logind (`session=11`) e mesmo VT do início ao fim, sem greeter e
sem senha. A pergunta que decidia o desenho — se o `uwsm` aceita subir uma
segunda vez dentro da mesma sessão depois da limpeza — está respondida: **aceita**,
desde que `graphical-session-pre.target` esteja parado e as unidades tenham
levado `reset-failed`. Toda transição passa obrigatoriamente por essa limpeza,
que é o que faltava.

A volta veio do próprio Big Picture: o shim registrou
`steamos-session-select plasma` às 17:35:19 e a sessão terminou 12 s depois.

Proteção contra laço infinito: dois compositores morrendo em menos de 15 s
seguidos e o wrapper devolve a sessão ao login manager em vez de girar.

### 8.5 Teste D, resolvido: o WirePlumber não move o som

Com o padrão devolvido ao fone USB antes de entrar, a sonda mediu dentro da
sessão console:

```
default-sink: ...KT_USB_Audio...analog-stereo  port=analog-output-headphones  state=RUNNING
```

O som tocou no fone, não na TV. A medição anterior parecia mostrar o contrário
só porque o WirePlumber restaura escolhas antigas entre boots — a escolha ainda
era do nosso hook. Portanto o mapeamento ELD→pin→perfil de card **é necessário**
e vira preparação obrigatória do modo console, não um hook opcional.

Confirmado por efeito no protótipo: `tv_key` do `couch.json` casa com
`monitor_name=SAMSUNG` no ELD pin 1, `output:hdmi-stereo-extra1` é aplicado ao
card, o sink aparece com `active_port=hdmi-output-1`, e a restauração devolve o
padrão anterior. O `pactl -f json list cards` continua escrevendo
`Invalid ASCII character` em stderr por causa das descrições acentuadas — o JSON
é válido, basta descartar stderr; `LC_ALL=C` não resolve, porque o texto vem do
próprio PulseAudio e não da localização do cliente.

## 9. O modo console, implementado (2026-08-31)

O couch mode foi removido inteiro. O que ficou no lugar é bem menor, porque a
maior parte do que ele fazia passou a ser do gamescope e do Steam.

### 9.1 Pacotes

- **`internal/audio`** — ELD → pin → porta → perfil de card, extraído do hook.
  Sobreviveu à remoção porque a lógica é sobre displays e som, não sobre couch.
- **`internal/apps`** — descoberta e fechamento gracioso de aplicativos, mais os
  candidatos para a lista. Ficou *mais* importante: entrar derruba o desktop.
- **`internal/console`** — detecção portátil (`DesktopNames=gamescope`, nunca
  nome de pacote), a limpeza do systemd, o protocolo de troca por arquivo, o
  preparo de áudio com estado em disco, e o laço hospedeiro.

### 9.2 Decisões que o spike impôs

**O laço hospedeiro em vez do gerenciador de login.** Uma sessão hospeda os dois
compositores; o login manager nunca vê a sessão terminar. Sem greeter, sem
senha, sem `RememberLastSession`, sem autologin, sem polkit, e funciona igual em
greetd, ly ou tty.

**O pedido de troca é um arquivo**, não um socket: o processo que pede está
prestes a ser morto pela troca que pediu.

**`StopCompositor` verifica por efeito.** `hyprctl dispatch` sai 0 e não faz nada
no parser Lua; confiar no código de retorno reportaria sucesso com o desktop
intacto.

**O preparo de áudio grava antes de mudar**, e nunca sobrescreve um registro
existente — entrar duas vezes gravaria a TV como escolha do desktop.

**A entrada é armada no daemon**, não executada por quem pediu: a contagem
regressiva precisa sobreviver ao painel que fecha e ao terminal que morre junto
com o desktop.

**`console setup` nunca edita arquivo de sistema.** Detecta o gerenciador de
login, escreve a entrada de sessão no diretório do usuário, e imprime a mudança
e como desfazê-la. Só SDDM foi testado, e ele diz isso.

### 9.3 O que some da configuração

Modo, HDR e VRR saíram: o gamescope pega o modo preferido do conector e o Steam
troca por jogo. `DeskDuringCouch` (`disabled`/`enabled`/`mirror`) saiu junto —
era a origem do aviso de arranjo do Steam e da barra sumida. O `console.json`
guarda a TV, a sessão de desktop para onde voltar, a lista de apps e o gatilho.

Migração: `MigrateFromCouch` semeia a partir do `couch.json` quando não há
`console.json`, preservando a TV e a lista de apps. O gatilho por controle
**não** é migrado: agora ele encerra a sessão, então precisa ser escolhido de
novo com isso em mente.
