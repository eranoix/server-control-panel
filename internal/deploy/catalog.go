package deploy

type Template struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Web         bool      `json:"web"`
	Port        int       `json:"port"`
	Compose     string    `json:"compose"`
	EnvHints    []EnvHint `json:"env_hints"`
}

type EnvHint struct {
	Key     string `json:"key"`
	Default string `json:"default"`
	Note    string `json:"note"`
	Secret  bool   `json:"secret"`
}

func Catalog() []Template {
	return []Template{
		{
			ID: "postgres", Name: "PostgreSQL 16", Category: "db",
			Description: "Relational database. Persistence on a named volume.",
			Web:         false, Port: 5432,
			EnvHints: []EnvHint{
				{Key: "POSTGRES_PASSWORD", Default: "", Note: "superuser password", Secret: true},
				{Key: "POSTGRES_DB", Default: "app", Note: "initial database"},
			},
			Compose: `services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB}
    ports:
      - "${PORT}:5432"
    volumes:
      - data:/var/lib/postgresql/data
    restart: unless-stopped
volumes:
  data:
`,
		},
		{
			ID: "redis", Name: "Redis 7", Category: "cache",
			Description: "In-memory cache/broker with AOF persistence.",
			Web:         false, Port: 6379,
			Compose: `services:
  redis:
    image: redis:7-alpine
    command: ["redis-server", "--appendonly", "yes"]
    ports:
      - "${PORT}:6379"
    volumes:
      - data:/data
    restart: unless-stopped
volumes:
  data:
`,
		},
		{
			ID: "minio", Name: "MinIO (S3)", Category: "storage",
			Description: "S3-compatible object storage. Web console + API.",
			Web:         true, Port: 9001,
			EnvHints: []EnvHint{
				{Key: "MINIO_ROOT_USER", Default: "admin", Note: "admin user"},
				{Key: "MINIO_ROOT_PASSWORD", Default: "", Note: "admin password (min 8)", Secret: true},
			},
			Compose: `services:
  minio:
    image: minio/minio:latest
    command: ["server", "/data", "--console-address", ":9001"]
    environment:
      MINIO_ROOT_USER: ${MINIO_ROOT_USER}
      MINIO_ROOT_PASSWORD: ${MINIO_ROOT_PASSWORD}
    ports:
      - "${PORT}:9001"
      - "9000:9000"
    volumes:
      - data:/data
    restart: unless-stopped
volumes:
  data:
`,
		},
		{
			ID: "uptime-kuma", Name: "Uptime Kuma", Category: "monitoring",
			Description: "Self-hosted uptime monitor and status page.",
			Web:         true, Port: 3001,
			Compose: `services:
  kuma:
    image: louislam/uptime-kuma:1
    ports:
      - "${PORT}:3001"
    volumes:
      - data:/app/data
    restart: unless-stopped
volumes:
  data:
`,
		},
		{
			ID: "umami", Name: "Umami Analytics", Category: "analytics",
			Description: "Privacy-first web analytics (requires a Postgres).",
			Web:         true, Port: 3000,
			EnvHints: []EnvHint{
				{Key: "DATABASE_URL", Default: "postgresql://umami:umami@db:5432/umami", Note: "Postgres connection"},
				{Key: "APP_SECRET", Default: "", Note: "session secret (random)", Secret: true},
			},
			Compose: `services:
  umami:
    image: ghcr.io/umami-software/umami:postgresql-latest
    environment:
      DATABASE_URL: ${DATABASE_URL}
      APP_SECRET: ${APP_SECRET}
    ports:
      - "${PORT}:3000"
    depends_on:
      - db
    restart: unless-stopped
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: umami
      POSTGRES_PASSWORD: umami
      POSTGRES_DB: umami
    volumes:
      - data:/var/lib/postgresql/data
    restart: unless-stopped
volumes:
  data:
`,
		},
		{
			ID: "adminer", Name: "Adminer", Category: "tool",
			Description: "Lightweight web UI to administer databases (Postgres/MySQL/…).",
			Web:         true, Port: 8080,
			Compose: `services:
  adminer:
    image: adminer:latest
    ports:
      - "${PORT}:8080"
    restart: unless-stopped
`,
		},
	}
}

func TemplateByID(id string) (Template, bool) {
	for _, t := range Catalog() {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}
