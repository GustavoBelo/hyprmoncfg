# Teste com a TV ligada

> **Estado em 2026-08-30, tarde.** Testes 1, 2, 3 e 6 **passaram**; faltam o 4 e
> o 5, que precisam de alguém jogando. O roteiro fica aqui porque é o que se
> repete a cada mudança de hardware — e porque rodá-lo revelou seis bugs que
> nenhum teste automatizado pegaria (`PLAN.md` §5).
>
> | # | | |
> |---|---|---|
> | 1 | Áudio | ✅ vai para o pin da TV e volta — **exigiu conserto**, ver abaixo |
> | 2 | Visual | ✅ tela cheia, sem overscan, legível do sofá, cores boas |
> | 3 | Resolução | ✅ **3840x2160@120** escolhido; estável, sem queda para 60 |
> | 4 | Voltar de um jogo | ⬜ pendente |
> | 5 | Controle | 🟡 a entrada disparou sozinha; o ciclo completo falta |
> | 6 | gamescope | ✅ instalado e exercitado, primeira execução do código |

O que sobrou depois da sessão de 2026-08-30. Todo o resto já foi exercitado com
a TV em standby; isto é só o que **exige a TV fisicamente acesa**.

Por quê: em standby o conector reporta `connected` (o EDID é legível), mas a TV
não apresenta ELD, então **não existe sink de áudio HDMI** e nada aparece na
tela. Confirme antes de começar:

```sh
grep -h monitor_present /proc/asound/card*/eld* | head -4
```

- `monitor_present1` → TV acordada, pode seguir.
- `monitor_present0` → ainda em standby, os testes 1 e 2 vão falhar por isso.

---

## Antes de começar

```sh
hyprmoncfg couch doctor
```

Tem que dizer `Ready for a console session`. Se reclamar de gerenciamento,
rode `hyprmoncfg manage`.

**Saída de emergência.** Se a tela ficar preta ou ilegível:

```sh
hyprmoncfg couch stop        # devolve o layout, o áudio, a barra e o idle
```

Se nem isso for possível, `Ctrl+Alt+F2` abre outro TTY e o comando acima
funciona de lá. E se a TV aceitar um modo que o cabo não aguenta, o apply
reverte sozinho em 10 s sem você fazer nada — é a rede que já existe.

---

## 1. Áudio vai para a TV e volta

**Passou, depois de um conserto.** Na primeira execução o som foi para o
**monitor da mesa**, e o comando reportou sucesso.

O porquê vale saber, porque o mesmo se repete em qualquer máquina com dois
displays numa placa só: a placa apresenta um pin de áudio por conector, e todo
sink derivado deles se descreve como a *placa* (`Navi 48 HDMI/DP Audio
Controller`), nunca como o display. Não dá para escolher pelo nome. Pior, a
placa expõe **um sink HDMI por vez**, então acender a TV não faz nascer um
segundo — é preciso trocar o perfil do card.

O elo que funciona é o EDID, via ELD:

```sh
for f in /proc/asound/card*/eld#*.*; do
  echo "== $f"; grep -E "monitor_present|monitor_name" "$f"
done
```

Aqui: `25G64` no pin 0 (mesa) e `SAMSUNG` no pin 1 (TV). O pin nomeia a porta
(`hdmi-output-N`) e a porta escolhe o perfil do card.

Para conferir, **olhe a porta, nunca o nome do sink**:

```sh
S=$(pactl get-default-sink)
pactl -f json list sinks | jq -r --arg s "$S" '.[]|select(.name==$s)|"port=\(.active_port)"'
```

O roteiro original:

```sh
pactl get-default-sink                    # anote: deve ser o KT USB
hyprmoncfg couch play
sleep 15
pactl get-default-sink                    # esperado: ...hdmi...
```

Toque algo antes de entrar e confirme que **o som acompanha a imagem** — o hook
move também os streams que já estavam tocando, não só o padrão.

```sh
hyprmoncfg couch stop
sleep 6
pactl get-default-sink                    # tem que voltar ao KT USB
```

Se o sink HDMI não aparecer, veja se ele existe:

```sh
pactl -f json list sinks | grep -o '"name": "[^"]*hdmi[^"]*"'
```

Sem resultado = a TV não está apresentando áudio, e o hook está certo em pular.

## 2. O resultado visual

Nada disso dá para verificar por comando — é olhar.

- O Big Picture preenche a tela inteira, sem tarja preta e sem cortar borda
  (overscan)? Se cortar, a TV está em modo "Zoom"/"16:9"; procure
  **Just Scan / Screen Fit / 1:1** nas configurações dela.
- Dá para **ler do sofá**? É o teste que decide se a resolução escolhida presta.
- Com HDR ligado: as cores ficam certas ou lavadas? Um HDR mal negociado deixa
  tudo cinzento. Se ficar ruim, desligue HDR na aba 4 e compare.

## 3. A decisão dos 3840x2160@120

Hoje seu layout está em **1920x1080@120**, herdado do perfil `game` antigo — que
era o contorno de espelhamento, não uma escolha. A heurística proporia
**3840x2160@120** (4K nativo a 120 Hz, confirmado na lista de modos da TV).

Na TUI, aba 4: selecione **TV mode** e ande com `←/→` até `3840x2160@120.00Hz`.
Depois `p` para entrar.

O que observar:

- A imagem **estabiliza**? 4K120 exige HDMI 2.1; um cabo insuficiente dá tela
  preta intermitente, piscadas ou queda para 60 Hz.
- Confirme o que realmente pegou:
  ```sh
  hyprctl -j monitors | jq -r '.[]|select(.name=="HDMI-A-1")|"\(.width)x\(.height)@\(.refreshRate)"'
  ```
  Se disser 60, a TV ou o cabo recusaram os 120.
- Texto do Big Picture a 4K: legível ou pequeno demais? Se pequeno,
  `2560x1440@120` é o meio-termo — foi o que você tinha escolhido à mão.

Escolha um e deixe. É o ajuste que separa "funciona" de "parece um console".

**Resultado:** `3840x2160@120` pegou e ficou estável — sem piscada e sem queda
para 60, então o cabo aguenta HDMI 2.1. Comparado lado a lado com 1080p120 e
escolhido por ser melhor, com o texto ainda legível do sofá. Fixado no
`couch.json`.

## 4. Voltar de um jogo

O bug que você já contornava à mão no `hyprland.lua`.

Entre no modo console, **abra um jogo, jogue um pouco e saia para o Big
Picture**. O Steam costuma voltar como janela flutuante 1100x700 (regra do
Omarchy em `/usr/share/omarchy/default/hypr/apps/steam.lua`).

Esperado: a sessão devolve o fullscreen em até ~2 s. Se não devolver:

```sh
hyprctl clients -j | jq -r '.[]|select(.class|test("steam";"i"))|"fs=\(.fullscreen) size=\(.size|join("x"))"'
```

`fs=0` com tamanho pequeno = o re-assert não pegou; vale reportar com o log de
`~/.local/state/hyprmoncfg/couch/couch.log`.

## 5. O controle liga o console

Precisa de um gamepad de verdade — não deu para simular.

Com `enter_on_controller_connect` ligado (já está), **ligue o controle** com o
desktop parado. Esperado: o modo console entra sozinho em poucos segundos.

```sh
journalctl --user -u hyprmoncfgd -f    # deixe rodando enquanto liga o controle
```

Deve aparecer `entering via a controller was connected`.

Para a saída automática: jogue **pelo menos 60 s** (é o uso mínimo, para não sair
por um controle que só piscou), depois desligue o controle. Em ~10 s de debounce
a sessão encerra e o desktop volta.

Confirme que o controle é visto:

```sh
hyprmoncfg couch doctor | grep controller
```

**Parcial.** A entrada disparou sozinha ao plugar o controle
(`couch: entering via a controller was connected`), mas a sessão não servia para
nada: o Steam morria com `Unable to open X11 display`, porque o daemon não
enxergava a sessão gráfica. Corrigido; o ciclo completo ainda não foi refeito.

Uma mudança de comportamento importa aqui: o gatilho agora segue a **borda** de
conexão, não a presença. Antes, com o controle plugado, qualquer `couch stop`
era desfeito no polling seguinte — não havia como sair do modo console. Então
para testar é preciso **desplugar e replugar**; com o controle já ligado, nada
acontece, e isso é o certo.

## 6. gamescope (opcional)

Não está instalado, e é pacote novo no sistema — por isso não instalei.

```sh
sudo pacman -S gamescope
```

Depois, na aba 4, ligue **gamescope** e ajuste o cap de frames. Ganha HDR e FSR
por jogo. O código monta a linha a partir do próprio layout do console
(resolução, refresh e `--hdr-enabled` quando o perfil tem `cm: hdr`).

**Passou.** Instalado (3.16.25) e executado pela primeira vez. A linha montada
foi exatamente a esperada, tirada do perfil sem nada digitado duas vezes:

```
/usr/bin/gamescope -f -W 3840 -H 2160 -r 120 --hdr-enabled -- /usr/bin/steam -gamepadui
```

Janela em `fs=2 size=3840x2160` na TV. Um detalhe do primeiro teste virou
conserto: **o gamescope não morria com a sessão** — sobrava uma janela
fullscreen que o Hyprland mudava para a mesa assim que a TV era desligada. Agora
o grupo de processos é encerrado no fim da sessão, e o `Reconcile` limpa um
abandonado depois de um crash.

Sob gamescope o Big Picture **não** é detectado, porque a classe da janela é
`gamescope`; a sessão cai em vigiar o processo do Steam. É o comportamento certo
e não vai mudar: sob gamescope existe uma janela só, cujo título vira o do jogo,
então casar por título leria "abriu um jogo" como "fechou o Big Picture".

---

## Se algo falhar

O log da sessão conta a história:

```sh
tail -30 ~/.local/state/hyprmoncfg/couch/couch.log
journalctl --user -u hyprmoncfgd --since "10 minutes ago"
```

Uma armadilha que apareceu seis vezes nesta base: **coisas que aceitam o comando,
saem com código 0 e não fazem nada** — `hyprctl keyword`, `hl.config()`,
`hyprctl dispatch` no parser Lua, `hl.dsp.window.move`, e o
`omarchy-shell notifications dnd`. Se algo parecer ignorado, verifique pelo
*efeito*, nunca pelo código de retorno.

E uma segunda, aprendida ao rodar este roteiro: **teste o caminho automático,
não o conveniente.** Todas as sessões da rodada anterior foram abertas por
`couch play` num terminal, e o terminal empresta um ambiente que o daemon não
tem — sessão Wayland, display X11, os helpers do Omarchy no PATH. Tudo passava.
Nenhum gatilho automático funcionava. Se um caminho é o que o usuário vai usar
de verdade, é por ele que se testa:

```sh
journalctl --user -u hyprmoncfgd -f     # e então plugue o controle
```
