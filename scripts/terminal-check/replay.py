"""Replaya os bytes CRUS da sessao num emulador de terminal de REFERENCIA.

Se a tela sair corrompida aqui, os bytes se contradizem sozinhos e o app e
inocente. Se sair limpa, quem diverge e o emulador do app.
"""
import sys

import pyte

LOG = "/opt/panel/data/users/sam/session-logs/Aplicativo.log"
COLS, ROWS = 67, 53
JANELA = int(sys.argv[1]) if len(sys.argv) > 1 else 1_500_000

dados = open(LOG, "rb").read()[-JANELA:]

tela = pyte.Screen(COLS, ROWS)
fluxo = pyte.Stream(tela)
fluxo.feed(dados.decode("utf-8", errors="replace"))

print(f"=== emulador de REFERENCIA (pyte) — {COLS}x{ROWS}, ultimos {len(dados)} bytes ===")
for i, linha in enumerate(tela.display):
    print(f"{i:02d}|{linha.rstrip()}")
