package redis_test

import (
	"testing"

	"github.com/boz/ephemerald"
	"github.com/boz/ephemerald/params"
	"github.com/boz/ephemerald/testutil"
	rredis "github.com/garyburd/redigo/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionExec(t *testing.T) {
	testutil.RunPoolFromFile(t, "pool.json", func(p params.Params) {
		db, err := rredis.DialURL(p.Url)
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Do("PING")
		require.NoError(t, err)
	})
}

func TestActionTruncate(t *testing.T) {
	testutil.WithPoolFromFile(t, "pool.json", func(pool ephemerald.Pool) {
		func() {
			p, err := pool.Checkout()
			require.NoError(t, err)
			defer pool.Return(p)

			db, err := rredis.DialURL(p.Url)
			require.NoError(t, err)
			defer db.Close()

			_, err = db.Do("SET", "testkey", "true")
			require.NoError(t, err)
		}()

		func() {
			p, err := pool.Checkout()
			require.NoError(t, err)
			defer pool.Return(p)

			db, err := rredis.DialURL(p.Url)
			require.NoError(t, err)
			defer db.Close()

			result, err := db.Do("GET", "testkey")
			require.NoError(t, err)
			assert.Empty(t, result)
		}()
	})
}
