#!/usr/bin/env python3
"""Valida a ESTRUTURA da documentacao tecnica antes de ela virar artefato/binario.

Motivacao (3 ocorrencias reais do mesmo defeito): a doc e um single-file HTML onde
cada topico vive num `<div class="page">` e a navegacao apenas alterna a
visibilidade dessas paginas. Um `</div>` a mais (ou um `<div class="card">` que
alguem esqueceu de abrir ao inserir uma entrada nova no historico) fecha a pagina
mais cedo -> todo o conteudo seguinte vira filho direto do `#main-content` e passa
a aparecer em CIMA DE TODAS as paginas. O HTML continua "valido" e o navegador nao
reclama; o estrago so aparece a olho nu.

Contar tags nao basta: o arquivo pode ficar com saldo zero e ainda assim estar com
o aninhamento trocado. Por isso aqui usa-se um parser de verdade (html.parser).

Uso:  scripts/check-docs-structure.py [arquivo.html ...]
Sai com codigo != 0 e explica o que consertar quando encontra problema.
"""
import sys
from html.parser import HTMLParser

VOID = {'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input',
        'link', 'meta', 'param', 'source', 'track', 'wbr'}
CONTAINER_ID = 'main-content'


class DocStructure(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.stack = []
        self.pages = []          # (id, linha_abre, linha_fecha)
        self.leaks = []          # conteudo solto direto no container
        self.unclosed = []

    def handle_starttag(self, tag, attrs):
        if tag in VOID:
            return
        d = dict(attrs)
        cls = d.get('class', '') or ''
        self.stack.append([tag, d.get('id', ''), cls, self.getpos()[0]])
        parent = self.stack[-2] if len(self.stack) >= 2 else None
        if parent and parent[1] == CONTAINER_ID and 'page' not in cls.split():
            self.leaks.append((self.getpos()[0], f'<{tag} class="{cls}">'))

    def handle_endtag(self, tag):
        if tag in VOID:
            return
        for i in range(len(self.stack) - 1, -1, -1):
            if self.stack[i][0] == tag:
                for el in self.stack[i:]:
                    if el[0] == 'div' and 'page' in el[2].split():
                        self.pages.append((el[1], el[3], self.getpos()[0]))
                del self.stack[i:]
                return

    def handle_data(self, data):
        if data.strip() and self.stack and self.stack[-1][1] == CONTAINER_ID:
            self.leaks.append((self.getpos()[0], repr(data.strip()[:60])))


def check(path):
    with open(path, encoding='utf-8') as fh:
        p = DocStructure()
        p.feed(fh.read())

    problems = []
    if not p.pages:
        problems.append(f'nenhum <div class="page"> encontrado (o #{CONTAINER_ID} mudou de forma?)')
    for line, what in p.leaks:
        problems.append(
            f'L{line}: {what} e filho DIRETO do #{CONTAINER_ID}, fora de qualquer .page '
            f'-> vai aparecer em TODAS as paginas')
    for el in p.stack:
        if el[0] in ('div', 'section', 'table'):
            problems.append(f'L{el[3]}: <{el[0]} class="{el[2]}"> nunca foi fechado')

    name = path.rsplit('/', 1)[-1]
    if problems:
        print(f'✗ {name}: estrutura invalida')
        for pr in problems[:15]:
            print(f'    {pr}')
        if len(problems) > 15:
            print(f'    … e mais {len(problems) - 15} ocorrencia(s)')
        print('    Causa tipica: entrada nova no historico inserida logo APOS um '
              '<div class="card"> ja existente, fechando com </div> proprio e '
              'deixando o card seguinte sem tag de abertura.')
        return False

    print(f'✓ {name}: {len(p.pages)} paginas, nenhum conteudo fora de .page')
    return True


if __name__ == '__main__':
    targets = sys.argv[1:] or ['.docs/Documentacao Tecnica - VPS Manager.html']
    sys.exit(0 if all([check(t) for t in targets]) else 1)
