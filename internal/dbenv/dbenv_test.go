package dbenv_test

import (
	"slices"
	"testing"

	"runova/internal/dbenv"
)

func TestEnvKindOf(t *testing.T) {
	cases := []struct {
		name string
		jar  bool
		want dbenv.Kind
	}{
		// The standard names keep the values they always had.
		{"DATABASE_URL", false, dbenv.URL},
		{"DATABASE_URL", true, dbenv.URL},
		{"SPRING_DATASOURCE_URL", false, dbenv.JDBC},
		{"SPRING_DATASOURCE_USERNAME", true, dbenv.User},
		{"SPRING_DATASOURCE_PASSWORD", true, dbenv.Password},
		{"PGHOST", false, dbenv.Host},
		{"PGPORT", false, dbenv.Port},
		{"PGUSER", false, dbenv.User},
		{"PGPASSWORD", false, dbenv.Password},
		{"PGDATABASE", false, dbenv.Database},

		// URLs: JDBC by name or for a JAR, R2DBC by name, postgres:// otherwise.
		{"DB_URL", false, dbenv.URL},
		{"DB_URL", true, dbenv.JDBC},
		{"db_url", false, dbenv.URL},
		{"DB_URI", false, dbenv.URL},
		{"DB_DSN", false, dbenv.URL},
		{"DB_DSN", true, dbenv.URL},
		{"DATABASE_CONNECTION_STRING", false, dbenv.URL},
		{"POSTGRES_URL", false, dbenv.URL},
		{"POSTGRESQL_URL", true, dbenv.JDBC},
		{"PG_URL", false, dbenv.URL},
		{"DBURL", false, dbenv.URL},
		{"DATASOURCE_URL", false, dbenv.JDBC},
		{"JDBC_URL", false, dbenv.JDBC},
		{"JDBC_DATABASE_URL", false, dbenv.JDBC},
		{"QUARKUS_DATASOURCE_JDBC_URL", true, dbenv.JDBC},
		{"SPRING_R2DBC_URL", true, dbenv.R2DBC},
		{"R2DBC_URL", false, dbenv.R2DBC},
		{"TYPEORM_URL", false, dbenv.URL},
		{"APP_DATABASE_URL", true, dbenv.JDBC},

		// Host, port, user, password, database name.
		{"DB_HOST", false, dbenv.Host},
		{"DB_HOSTNAME", false, dbenv.Host},
		{"DB_SERVER", false, dbenv.Host},
		{"DBHOST", false, dbenv.Host},
		{"POSTGRES_HOST", false, dbenv.Host},
		{"TYPEORM_HOST", false, dbenv.Host},
		{"DB_PORT", false, dbenv.Port},
		{"POSTGRES_PORT", false, dbenv.Port},
		{"DB_USER", false, dbenv.User},
		{"DB_USERNAME", false, dbenv.User},
		{"DB_LOGIN", false, dbenv.User},
		{"DBUSER", false, dbenv.User},
		{"POSTGRES_USER", false, dbenv.User},
		{"QUARKUS_DATASOURCE_USERNAME", true, dbenv.User},
		{"SPRING_R2DBC_USERNAME", true, dbenv.User},
		{"TYPEORM_USERNAME", false, dbenv.User},
		{"DB_PASS", false, dbenv.Password},
		{"DB_PASSWORD", false, dbenv.Password},
		{"DB_PWD", false, dbenv.Password},
		{"DB_PASSWD", false, dbenv.Password},
		{"DBPASS", false, dbenv.Password},
		{"POSTGRES_PASSWORD", false, dbenv.Password},
		{"TYPEORM_PASSWORD", false, dbenv.Password},
		{"DB_NAME", false, dbenv.Database},
		{"DBNAME", false, dbenv.Database},
		{"DB_DATABASE", false, dbenv.Database},
		{"DATABASE_NAME", false, dbenv.Database},
		{"POSTGRES_DB", false, dbenv.Database},
		{"PG_DB", false, dbenv.Database},
		{"TYPEORM_DATABASE", false, dbenv.Database},
		{"SQL_PASSWORD", false, dbenv.Password},

		// Not about this PostgreSQL database: left to the user.
		{"REDIS_URL", false, ""},
		{"REDIS_DB", false, ""},
		{"REDIS_PASSWORD", false, ""},
		{"MONGO_URL", false, ""},
		{"MONGODB_URI", false, ""},
		{"MONGO_DB_URL", false, ""},
		{"MYSQL_URL", false, ""},
		{"MYSQL_PASSWORD", false, ""},
		{"MYSQL_DB_HOST", false, ""},
		{"MARIADB_URL", false, ""},
		{"MSSQL_URL", false, ""},
		{"SQL_SERVER", false, ""},
		{"SQLITE_DB_PATH", false, ""},
		{"API_URL", false, ""},
		{"JWT_SECRET", false, ""},
		{"PORT", false, ""},
		{"HOST", false, ""},
		{"URL", false, ""},
		{"PASSWORD", false, ""},
		{"DATABASE", false, ""},
		{"DB", false, ""},
		{"USER", false, ""},
		{"SMTP_PASSWORD", false, ""},
		{"ADMIN_PASSWORD", false, ""},
		{"SERVER_PORT", false, ""},

		// Same words, other meaning or ambiguous: left to the user.
		{"DB_SCHEMA", false, ""},
		{"DB_POOL_SIZE", false, ""},
		{"DB_SSL_MODE", false, ""},
		{"DB_DRIVER", false, ""},
		{"DB_DIALECT", false, ""},
		{"TYPEORM_CONNECTION", false, ""},
		{"SPRING_DATASOURCE_DRIVER_CLASS_NAME", true, ""},
		{"SPRING_DATASOURCE_HIKARI_MAXIMUM_POOL_SIZE", true, ""},
		{"DB_MAX_CONNECTIONS", false, ""},
		{"DB_HOST_PORT", false, ""},
		{"TEST_DB_URL", false, ""},
		{"DB_TEST_URL", false, ""},
		{"DB_REPLICA_HOST", false, ""},
		{"DB_READ_URL", false, ""},
		{"DB_ADMIN_PASSWORD", false, ""},
		{"DB_ROOT_PASSWORD", false, ""},
		{"POSTGRES_ADMIN_USER", false, ""},
		{"SHADOW_DATABASE_URL", false, ""},
		{"MY_BILLING_DB_URL", false, ""},
		{"BILLING_DB_URL", false, ""},
		{"DB_TIMEOUT", false, ""},
		{"DB_CONNECTION_TIMEOUT", false, ""},
		{"DB_URL_PARAMS", false, ""},
		{"SPRING_APPLICATION_NAME", true, ""},
		{"POSTGRES_HOST_AUTH_METHOD", false, ""},
	}
	for _, c := range cases {
		if got := dbenv.KindOf(c.name, c.jar); got != c.want {
			t.Errorf("EnvKindOf(%q, jar=%v) = %q, want %q", c.name, c.jar, got, c.want)
		}
	}
}

func TestVarsDetected(t *testing.T) {
	got := dbenv.Vars([]string{"REDIS_URL", "DB_URL", "DATABASE_URL", "DB_PASSWORD", "DB_URL", "JWT_SECRET"}, true)
	n := len(dbenv.StandardNames())
	want := []dbenv.Var{{Name: "DB_URL", Kind: dbenv.JDBC}, {Name: "DB_PASSWORD", Kind: dbenv.Password}}
	if !slices.Equal(got[n:], want) {
		t.Fatalf("detected = %+v, want %+v", got[n:], want)
	}
	if got := dbenv.Vars(nil, false); len(got) != n {
		t.Fatalf("Vars(nil) = %+v, want only the standard names", got)
	}
}

func TestVarsStandardFirst(t *testing.T) {
	got := dbenv.Vars([]string{"PGHOST", "DB_HOST", "MONGO_URL"}, false)
	var standard, names []string
	for _, v := range got {
		names = append(names, v.Name)
		if v.Standard {
			standard = append(standard, v.Name)
		}
	}
	if want := append(dbenv.StandardNames(), "DB_HOST"); !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if !slices.Equal(standard, dbenv.StandardNames()) {
		t.Fatalf("standard = %v", standard)
	}
}
