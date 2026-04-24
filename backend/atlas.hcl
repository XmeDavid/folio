// Atlas configuration for Folio migrations.
// https://atlasgo.io/guides/postgres

env "local" {
  url = getenv("DATABASE_URL")
  dev = "docker://postgres/17/dev?search_path=public"
  migration {
    dir = "file://db/migrations"
  }
  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}

env "prod" {
  url = getenv("DATABASE_URL")
  migration {
    dir = "file://db/migrations"
  }
}
