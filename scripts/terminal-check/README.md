# Provar de que lado está o defeito do terminal, sem aparelho

Quando o dono relatar a tela do terminal errada — embaralhada, duplicada,
espremida — **rode isto ANTES de formular qualquer hipótese**. São dois
instrumentos, e juntos eles separam as três camadas em que o defeito pode
morar: os **bytes**, o **motor** e o **desenho**.

Foi a falta deles que fez duas correções especulativas serem publicadas em
2026-09-10, e foi a presença deles que achou a causa em vinte minutos.

## 1. Os bytes se contradizem? (`replay.py`)

Passa o log cru da sessão por um emulador de terminal **de referência**
(`pyte`), independente do nosso.

```sh
python3 -m venv /tmp/vt && /tmp/vt/bin/pip install pyte
/tmp/vt/bin/python replay.py 1500000
```

- Tela **corrompida** → os bytes se contradizem sozinhos; o app é fiel e o
  problema é do lado do servidor ou do programa remoto.
- Tela **limpa** → os bytes estão certos. Siga para o passo 2.

## 2. O motor do app diverge? (`harness.cpp`)

Roda o **mesmo `libghostty-vt.a` que vai no aparelho** aqui na VPS. O `.a` é
compilado para bionic, então falta só `__errno` — é o que `shim_errno.cpp`
fornece.

```sh
g++ -std=c++17 -O1 \
    -I ../../android/terminal-engine/vendor/include \
    harness.cpp shim_errno.cpp \
    ../../android/terminal-engine/vendor/x86_64/libghostty-vt.a \
    -o harness

./harness /opt/panel/data/users/sam/session-logs/<Sessao>.log 67 53 1500000
```

Argumentos: `<log> <colunas> <linhas> <janela-em-bytes> [tamanho-do-pedaco]`.
O último reproduz o replay **em pedaços** que o app faz no primer.

- Diverge do `pyte` → o defeito é do motor ou do shim JNI.
- Igual ao `pyte` (as duas limpas) → **o defeito está no que o app ALIMENTA no
  motor**, ou no desenho. Siga para o passo 3.

## 3. O app alimenta errado?

Foi aqui que o defeito de 2026-09-10 estava. Componha uma entrada que imite a
suspeita e passe pelo `harness`. Para a duplicata que o primer causava:

```sh
python3 -c "
import io
d = io.open('/opt/.../Aplicativo.log','rb').read()[-1500000:]
io.open('sobreposto.bin','wb').write(d + d[-2000:])"
./harness sobreposto.bin 67 53 99999999
```

**Atenção ao tamanho da sobreposição.** 2 KiB corrompem; 20 KiB e 100 KiB não —
um pedaço grande repinta um quadro inteiro por cima e a tela se recompõe, e é o
pedaço curto, meio quadro, que fica. Testar só com valores grandes conclui
"não é isso" e está errado.

## A armadilha que custou uma versão publicada

O log da sessão contém **o que a própria sessão escreveu na tela**. Se você
descreve o texto corrompido numa mensagem, ele aparece no log — e um `grep`
depois o devolve como se fosse evidência. **Antes de tratar um trecho do log
como prova, confirme que não é eco do que você mesmo escreveu.**
