// Commande dbtool exporte et restaure la base PostgreSQL depuis le container web.
//
// Usage :
//
//	dbtool export [-dsn $DATABASE_URL] [-out boussole.sql.gz] [-no-gzip] [-schema-only|-data-only]
//	dbtool import [-dsn $DATABASE_URL] [-in boussole.sql.gz] [-dry-run] [-no-transaction]
//
// export : pg_dump (SQL, --clean --if-exists) compressé en gzip vers stdout ou un fichier.
// import : restaure le dump via psql (ON_ERROR_STOP, une seule transaction).
//
//	Le format gzip est détecté automatiquement ; un SQL brut est aussi accepté.
//	-dry-run exécute la restauration dans une transaction annulée (ROLLBACK).
//
// Exemples (depuis l'hôte) :
//
//	docker exec boussole-web dbtool export > boussole.sql.gz
//	docker exec -i boussole-web dbtool import -dry-run < boussole.sql.gz
//	docker exec -i boussole-web dbtool import < boussole.sql.gz
package main

import (
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	var err error
	switch os.Args[1] {
	case "export":
		err = runExport(ctx, os.Args[2:], log)
	case "import":
		err = runImport(ctx, os.Args[2:], log)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue : %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("échec", "cmd", os.Args[1], "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `dbtool — export/import de la base boussole

Commandes :
  export   pg_dump → gzip (stdout par défaut)
  import   gzip/SQL (stdin par défaut) → psql

  dbtool export [-dsn $DATABASE_URL] [-out -] [-no-gzip] [-schema-only] [-data-only]
  dbtool import [-dsn $DATABASE_URL] [-in -] [-dry-run] [-no-transaction]

Exemples :
  docker exec boussole-web dbtool export > boussole.sql.gz
  docker exec boussole-web dbtool export -out /data/boussole.sql.gz
  docker exec -i boussole-web dbtool import -dry-run < boussole.sql.gz
  docker exec -i boussole-web dbtool import < boussole.sql.gz
`)
}

// --- export ---

func runExport(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("dbtool export", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "DSN PostgreSQL (ou $DATABASE_URL)")
	out := fs.String("out", "-", "fichier de sortie, - pour stdout")
	noGzip := fs.Bool("no-gzip", false, "écrire du SQL brut (sans compression gzip)")
	schemaOnly := fs.Bool("schema-only", false, "exporter uniquement le schéma")
	dataOnly := fs.Bool("data-only", false, "exporter uniquement les données")
	_ = fs.Parse(args)

	if *schemaOnly && *dataOnly {
		return errors.New("-schema-only et -data-only sont incompatibles")
	}
	dsnVal := dsnOrDefault(*dsn)

	env, dbname, err := pgEnv(dsnVal)
	if err != nil {
		return err
	}

	pgArgs := []string{"--no-owner", "--no-privileges", "--clean", "--if-exists", "--format=plain"}
	switch {
	case *schemaOnly:
		pgArgs = append(pgArgs, "--schema-only")
	case *dataOnly:
		pgArgs = append(pgArgs, "--data-only")
	}
	if dbname != "" {
		pgArgs = append(pgArgs, dbname)
	}

	w, closeOut, err := openOutput(*out, !*noGzip)
	if err != nil {
		return err
	}

	counter := &countingWriter{w: w}
	cmd := exec.CommandContext(ctx, bin("PG_DUMP_BIN", "pg_dump"), pgArgs...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = counter
	cmd.Stderr = os.Stderr
	log.Info("pg_dump", "args", strings.Join(pgArgs, " "), "out", *out)
	if err := cmd.Run(); err != nil {
		_ = closeOut()
		return fmt.Errorf("pg_dump: %w", err)
	}
	if err := closeOut(); err != nil {
		return err
	}
	log.Info("export terminé", "octets", counter.n, "gzip", !*noGzip)
	return nil
}

// --- import ---

func runImport(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("dbtool import", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "DSN PostgreSQL (ou $DATABASE_URL)")
	in := fs.String("in", "-", "fichier d'entrée, - pour stdin")
	dryRun := fs.Bool("dry-run", false, "exécuter la restauration dans une transaction annulée")
	noTx := fs.Bool("no-transaction", false, "ne pas envelopper la restauration dans une transaction")
	_ = fs.Parse(args)

	dsnVal := dsnOrDefault(*dsn)

	env, dbname, err := pgEnv(dsnVal)
	if err != nil {
		return err
	}

	body, closeIn, err := openInput(*in)
	if err != nil {
		return err
	}
	defer closeIn()

	psqlArgs := []string{"-v", "ON_ERROR_STOP=1", "--quiet"}
	if *dryRun {
		// La transaction est ouverte puis annulée : le serveur rollback aussi
		// automatiquement si psql s'arrête sur erreur en cours de route.
	} else if !*noTx {
		psqlArgs = append(psqlArgs, "--single-transaction")
	}
	if dbname != "" {
		psqlArgs = append(psqlArgs, dbname)
	}

	var stdin io.Reader = body
	if *dryRun {
		stdin = io.MultiReader(
			strings.NewReader("BEGIN;\n"),
			body,
			strings.NewReader("\nROLLBACK;\n"),
		)
	}

	cmd := exec.CommandContext(ctx, bin("PSQL_BIN", "psql"), psqlArgs...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	mode := "restauration"
	if *dryRun {
		mode = "simulation (ROLLBACK)"
	}
	log.Info("psql", "mode", mode, "in", *in)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql: %w", err)
	}
	if *dryRun {
		log.Info("simulation terminée, base inchangée")
	} else {
		log.Info("import terminé")
	}
	return nil
}

// --- connexion ---

func dsnOrDefault(dsn string) string {
	if dsn != "" {
		return dsn
	}
	return "postgres://boussoule:changeme@localhost:5432/boussole?sslmode=disable"
}

// pgEnv décompose le DSN en variables PG* pour éviter de passer le mot de
// passe en argument de ligne de commande. Retourne aussi l'éventuel argument
// dbname (DSN non-URL passé tel quel à pg_dump/psql).
func pgEnv(dsn string) (env []string, dbname string, err error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		// DSN au format libpq key=value : passé tel quel en argument.
		return nil, dsn, nil
	}
	host := u.Hostname()
	if host == "" {
		return nil, "", errors.New("DSN sans hôte")
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	env = []string{"PGHOST=" + host, "PGPORT=" + port, "PGCONNECT_TIMEOUT=10"}
	if u.User != nil {
		env = append(env, "PGUSER="+u.User.Username())
		if pass, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pass)
		}
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		env = append(env, "PGDATABASE="+db)
	}
	if ssl := u.Query().Get("sslmode"); ssl != "" {
		env = append(env, "PGSSLMODE="+ssl)
	}
	return env, "", nil
}

func bin(envKey, def string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return def
}

// --- flux ---

// openOutput ouvre la destination ; gzip activé sauf noGzip. Retourne aussi la
// fonction de fermeture (à appeler via defer puis explicitement pour vider gzip).
func openOutput(path string, gz bool) (io.Writer, func() error, error) {
	var wc io.WriteCloser = writeNopCloser{os.Stdout}
	if path != "-" {
		f, err := os.Create(path)
		if err != nil {
			return nil, nil, err
		}
		wc = f
	}
	closeBase := wc.Close

	if gz {
		gz := gzip.NewWriter(wc)
		return gz, func() error {
			if err := gz.Close(); err != nil {
				_ = closeBase()
				return err
			}
			return closeBase()
		}, nil
	}
	return wc, closeBase, nil
}

// openInput ouvre la source et décompresse le gzip si le flux en est un
// (détection des deux octets de signature).
func openInput(path string) (io.Reader, func() error, error) {
	var rc io.ReadCloser = readNopCloser{os.Stdin}
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		rc = f
	}

	head := make([]byte, 2)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		_ = rc.Close()
		return nil, nil, err
	}
	rest := io.MultiReader(strings.NewReader(string(head[:n])), rc)

	if n == 2 && head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(rest)
		if err != nil {
			_ = rc.Close()
			return nil, nil, err
		}
		return gz, func() error {
			_ = gz.Close()
			return rc.Close()
		}, nil
	}
	return rest, rc.Close, nil
}

type writeNopCloser struct{ io.Writer }

func (writeNopCloser) Close() error { return nil }

type readNopCloser struct{ io.Reader }

func (readNopCloser) Close() error { return nil }

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
