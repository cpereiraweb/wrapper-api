# Implantando na Kronn

A imagem é construída pelo GitHub Actions a cada push na `main`, publicada no
GHCR e implantada na Kronn pelo deploy hook. O caminho inteiro:

```
push na main → ci.yml, uma execução só, em sequência:
  1. fmt, vet, testes, link cgo, govulncheck, lint, segredo   (em paralelo)
  2. job publicar (publicar.yml), só se TODOS passarem:
       build → :<sha> → teste de fumaça → :latest → deploy hook
                                                        │
Kronn (app nfe-api, Docker sem Swarm)  ◀──────────────┘
  docker pull ghcr.io/cpereiraweb/wrapper-api/api:latest
  docker compose down --remove-orphans
  docker compose up -d --force-recreate --remove-orphans
```

Commit reprovado em qualquer gate não gera imagem. E o `:latest` só anda
depois de o teste de fumaça provar que a imagem sobe e que a lib nativa carrega
nos cinco serviços: imagem que falhou ali nunca chega à Kronn, nem por um
redeploy manual. Para republicar um commit, re-execute o CI dele.

Cada deploy derruba e sobe os dois containers: são alguns segundos sem serviço
(o worker carrega a lib e só então a API sobe). Uma transmissão em curso não é
cortada: o `stop_grace_period` de 75s cobre os 60s que o shutdown espera por
ela. Quem chegar nessa janela recebe erro do Traefik, sem o corpo JSON de erro
da API, e pode repetir: a requisição não chegou ao serviço.

## Uma vez, no GitHub

Os três passos exigem **admin** do repositório `cpereiraweb/wrapper-api`.

1. **Ligar o Actions no fork.** Em fork, os workflows nascem desligados: aba
   *Actions*, "I understand my workflows, go ahead and enable them".
2. **Deixar o pacote legível pela Kronn.** O primeiro push na `main` cria o
   pacote `wrapper-api/api` em *Packages* do dono. O mais simples é torná-lo
   público (*Package settings* → *Change visibility*): o código já é público,
   sob AGPL, e a imagem não carrega segredo. Se ficar privado, a credencial da
   Kronn precisa ser de alguém com leitura nele.
3. **Cadastrar o secret `KRONN_DEPLOY_HOOK`** (*Settings* → *Secrets and
   variables* → *Actions*) com a URL do deploy hook da app, que sai do passo 4
   abaixo. Sem ele o workflow publica a imagem e para, com um aviso.

A URL do hook é a autenticação: quem a tiver dispara deploy. Ela vive só no
secret, nunca em arquivo, issue ou chat.

## Uma vez, na Kronn

1. **Credencial do registry.** *Container Registry Credentials* → GHCR,
   `ghcr.io`, usuário do GitHub e um token clássico com `read:packages`. A Kronn
   exige credencial para o `ghcr.io` mesmo com o pacote público, e aborta o
   deploy sem ela.
2. **Criar a app Docker**, modo **imagem**:

   | Campo | Valor |
   |---|---|
   | Nome | `nfe-api` (vira o slug, e o compose depende dele) |
   | Imagem | `ghcr.io/cpereiraweb/wrapper-api/api` |
   | Tag | `latest` |
   | Porta interna | `8080` (o default da Kronn é 80) |
   | Domínio | `nfe.datasapiens.ia.br`, com SSL ligado |
   | Health check | desligado: o compose já traz os dois |
   | Volumes | nenhum: o compose declara o dele |
   | Autenticação | manual, com a credencial do passo 1 |

   Imagem e tag têm que ser idênticas ao `image:` do compose. A Kronn faz o
   `docker pull` pelo que está no painel e implanta pelo que está no compose,
   e não reescreve um a partir do outro.
3. **Compose:** troque o que a Kronn gerou (só a API, sem worker nem volume)
   pelo [`docker-compose.yml`](docker-compose.yml) deste diretório. Ele já traz
   os labels do Traefik, então a Kronn o usa como está: se mudar o domínio no
   painel, mude também a linha do `Host(...)` aqui.
4. **`.env`:** cole o bloco abaixo e preencha o `API_TOKEN`. Ligue o
   **auto-deploy** e copie a URL do deploy hook para o secret do GitHub.
5. **DNS:** aponte o domínio para o servidor. O Traefik da Kronn emite o
   certificado Let's Encrypt no primeiro acesso.
6. **Primeiro deploy** pelo painel, e confira:

   ```bash
   curl -fsS https://nfe.datasapiens.ia.br/readyz   # {"status":"ok"}: o worker carregou a lib
   ```

### O `.env`

```bash
# Obrigatório: com MODO=producao a API recusa subir sem ele.
# Gere com: openssl rand -hex 32
API_TOKEN=

# producao endurece o boot (exige token, recusa log que grava certificado).
# NÃO escolhe o ambiente da SEFAZ: esse vai no payload, por requisição.
MODO=producao

# Atrás do Traefik o peer da conexão é sempre o próprio Traefik. Sem isto, todos
# os chamadores dividem um único balde do limite por endereço.
TRUST_PROXY_HEADERS=true
API_RATE_PER_MIN=240
MAX_BODY_BYTES=8388608

# Concorrência fiscal = 1 worker × slots. Cada slot a mais é uma requisição a
# mais que um crash da lib leva junto.
ACBR_WORKER_SLOTS=1
ACBR_WORKER_TIMEOUT_SECONDS=90
ACBR_WORKER_MAX_CALLS=0
```

`ACBR_WORKERS` e `ACBR_WORKER_LISTEN` estão fixos no compose, e é lá que devem
ficar: são o contrato entre os dois serviços, não configuração.

Se o único cliente for o backend de um sistema, todas as chamadas saem do mesmo
endereço e dividem o mesmo balde: dimensione `API_RATE_PER_MIN` para ele.

## O que não fazer no painel

- **Não use os editores de Volumes, Health check e Recursos desta app.** Eles
  reescrevem só o serviço principal e trocam o `volumes:` dele pelo do painel: a
  API perde o socket e fica em 503.
- **Não crie outro `worker` apontando para o mesmo socket.** Todos escutariam o
  mesmo caminho e só o último atenderia. Mais vazão é `ACBR_WORKER_SLOTS`.
- **Não use o rollback do painel.** Ele troca a tag no painel, mas o compose
  continua em `:latest`. Veja "Voltando uma versão" abaixo.

## Voltando uma versão

Toda imagem publicada continua no GHCR com a tag do commit. Para voltar,
aponte `:latest` para o commit bom e reimplante:

```bash
docker buildx imagetools create \
  -t ghcr.io/cpereiraweb/wrapper-api/api:latest \
     ghcr.io/cpereiraweb/wrapper-api/api:<sha-do-commit-bom>
```

e dispare o deploy pelo painel. O próximo push na `main` volta a andar com o
`:latest`, então reverta o commit problemático antes.

## Antes do primeiro documento de verdade

O envio à SEFAZ não é exercitado em CI, porque exige certificado A1 real (ver
[docs/LIMITACOES.md](../../docs/LIMITACOES.md)). Transmita primeiro com
`"ambiente": "homologacao"` no payload, confira protocolo, `cstat` e o `verProc`
do XML autorizado, e só então passe a mandar `producao`.
