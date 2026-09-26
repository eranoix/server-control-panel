# Configuração do Prometheus / Alertmanager / Grafana

**Estes arquivos são a fonte da verdade.** O que está no ar é uma cópia deles em
`/opt/projetos/vpsmanager/deploy/observability/`, que **não é um repositório
git** — até 2026-09-07 as regras de alerta desta VPS existiam só ali, sem
histórico, sem cópia e sem revisão. Uma regra apagada por engano não teria como
ser recuperada.

## Publicar uma mudança

```bash
bash scripts/publicar-observabilidade.sh
```

O script copia daqui para o diretório vivo e **recria** o container do
Prometheus. Recriar não é zelo excessivo:

> O arquivo de regras é **bind mount de ARQUIVO**. Editor que escreve-e-renomeia
> troca o inode, e o container continua enxergando o arquivo antigo. O
> `POST /prometheus/-/reload` devolve **HTTP 200 e não carrega nada** — foi
> medido. Só recriar o container refaz o mount.

O script confere, depois de publicar, que o container está mesmo vendo o número
de alertas que o arquivo tem.

## O que mora aqui

| arquivo | o quê |
|---|---|
| `prometheus.yml` | scrape targets e onde ficam as regras |
| `prometheus-rules.yml` | os alertas — três grupos: `vpsmanager`, `host`, `containers` |
| `alertmanager.yml` | roteamento; o único receptor é o webhook de loopback do próprio vps-manager (`/_internal/alert`), sem segredo nenhum |
| `docker-compose.yml` | prometheus, grafana, node-exporter, cadvisor, alertmanager |

## Alertas de swap

Os três alertas de swab existem por causa de um incidente real: o swap chegou a 100% e
nenhuma regra viu, porque `HostMemoryLow` olha `MemAvailable` (que estava
folgado) e `ContainerMemoryNearLimit` só dispara acima de 90% do limite (o
culpado estava em 56% dele justamente por poder despejar o resto no swap). Ver
`docs/infra-swap-e-limites-de-container.md`.
