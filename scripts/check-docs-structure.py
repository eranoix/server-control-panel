#!/usr/bin/env python3
"""Validates the STRUCTURE of the technical docs before they become an artifact.

The docs are a single-file HTML where each topic lives in a `<div class="page">`
and navigation only toggles their visibility. One extra `</div>` closes a page
early, so everything after it becomes a direct child of `#main-content` and shows
on TOP OF EVERY page. The HTML stays "valid", so counting tags is not enough; a
real parser (html.parser) checks the nesting.

Usage:  scripts/check-docs-structure.py [file.html ...]
Exits non-zero and explains what to fix when it finds a problem.
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
        self.pages = []          # (id, open_line, close_line)
        self.leaks = []          # loose content directly in the container
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
        problems.append(f'no <div class="page"> found (did #{CONTAINER_ID} change shape?)')
    for line, what in p.leaks:
        problems.append(
            f'L{line}: {what} is a DIRECT child of #{CONTAINER_ID}, outside any .page '
            f'-> it will show on EVERY page')
    for el in p.stack:
        if el[0] in ('div', 'section', 'table'):
            problems.append(f'L{el[3]}: <{el[0]} class="{el[2]}"> was never closed')

    name = path.rsplit('/', 1)[-1]
    if problems:
        print(f'✗ {name}: invalid structure')
        for pr in problems[:15]:
            print(f'    {pr}')
        if len(problems) > 15:
            print(f'    … and {len(problems) - 15} more')
        print('    Typical cause: a new history entry inserted right AFTER an existing '
              '<div class="card">, closed with its own </div>, leaving the next card '
              'without an opening tag.')
        return False

    print(f'✓ {name}: {len(p.pages)} pages, no content outside .page')
    return True


if __name__ == '__main__':
    targets = sys.argv[1:] or ['.docs/Documentacao Tecnica - Server Control Panel.html']
    sys.exit(0 if all([check(t) for t in targets]) else 1)
