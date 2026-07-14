// Package wellknown carries a curated table of ports that developer
// machines commonly have strong opinions about: framework dev-server
// defaults, databases, brokers, and observability stacks. portberth
// avoids assigning these and quotes them when explaining a conflict.
package wellknown

import "sort"

// Service describes why a port is notable.
type Service struct {
	Name string // short identifier, e.g. "postgresql"
	Desc string // one-line human description
}

// table is the curated port knowledge. Kept small and honest: only ports
// a developer plausibly collides with on a laptop, not the full IANA list.
var table = map[int]Service{
	22:    {"ssh", "OpenSSH server"},
	25:    {"smtp", "mail transfer (SMTP)"},
	53:    {"dns", "DNS resolver"},
	80:    {"http", "plain HTTP"},
	443:   {"https", "HTTP over TLS"},
	1025:  {"smtp-dev", "local SMTP catcher convention (MailHog/Mailpit)"},
	1313:  {"hugo", "Hugo dev server default"},
	1433:  {"mssql", "Microsoft SQL Server"},
	1883:  {"mqtt", "MQTT broker (Mosquitto)"},
	2379:  {"etcd", "etcd client API"},
	2380:  {"etcd-peer", "etcd peer traffic"},
	3000:  {"node-dev", "Node dev-server default (Express, Next.js, Rails 7+)"},
	3001:  {"node-dev-alt", "common fallback when 3000 is taken"},
	3306:  {"mysql", "MySQL / MariaDB server"},
	4000:  {"phoenix", "Phoenix / Jekyll dev server default"},
	4200:  {"angular", "Angular CLI dev server default"},
	4317:  {"otlp-grpc", "OpenTelemetry collector, OTLP/gRPC"},
	4318:  {"otlp-http", "OpenTelemetry collector, OTLP/HTTP"},
	5000:  {"flask", "Flask dev default; AirPlay Receiver on macOS"},
	5173:  {"vite", "Vite dev server default"},
	5432:  {"postgresql", "PostgreSQL database server"},
	5601:  {"kibana", "Kibana web UI"},
	5672:  {"amqp", "RabbitMQ broker (AMQP)"},
	6006:  {"tensorboard", "TensorBoard web UI"},
	6379:  {"redis", "Redis server"},
	7700:  {"meilisearch", "Meilisearch HTTP API"},
	8000:  {"http-alt", "Django runserver / python -m http.server default"},
	8025:  {"mail-ui", "MailHog / Mailpit web UI"},
	8080:  {"http-proxy", "generic HTTP alternate / proxies / Tomcat"},
	8081:  {"http-alt-2", "second generic HTTP alternate"},
	8443:  {"https-alt", "HTTPS alternate"},
	8888:  {"jupyter", "Jupyter Notebook / Lab default"},
	9000:  {"php-fpm", "PHP-FPM / MinIO / SonarQube default"},
	9090:  {"prometheus", "Prometheus web UI and API"},
	9092:  {"kafka", "Apache Kafka broker"},
	9200:  {"elasticsearch", "Elasticsearch HTTP API"},
	9411:  {"zipkin", "Zipkin collector and UI"},
	11211: {"memcached", "Memcached server"},
	15672: {"rabbitmq-ui", "RabbitMQ management UI"},
	27017: {"mongodb", "MongoDB server"},
}

// Lookup reports whether a port is well-known and, if so, what for.
func Lookup(port int) (Service, bool) {
	s, ok := table[port]
	return s, ok
}

// Ports returns every well-known port in ascending order, for docs,
// tests, and deterministic iteration.
func Ports() []int {
	out := make([]int, 0, len(table))
	for p := range table {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
