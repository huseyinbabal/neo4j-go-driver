/*
 * Copyright (c) 2002-2020 "Neo4j,"
 * Neo4j Sweden AB [http://neo4j.com]
 *
 * This file is part of Neo4j.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 */

package router

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/neo4j/internal/db"
	poolpackage "github.com/neo4j/neo4j-go-driver/neo4j/internal/pool"
)

// Verifies that concurrent access works as expected relying on the race detector to
// report supicious behavior.
func TestMultithreading(t *testing.T) {
	wg := sync.WaitGroup{}
	wg.Add(2)

	// Setup a router that needs to read the routing table essentially on every access to
	// stress threading a bit more.
	num := 0
	table := &db.RoutingTable{Readers: []string{"rd1", "rd2"}, Writers: []string{"wr"}, TimeToLive: 1}
	pool := &poolFake{
		borrow: func(names []string, cancel context.CancelFunc) (poolpackage.Connection, error) {
			num++
			return &connFake{table: table}, nil
		},
	}
	n := time.Now()
	router := New("router", func() []string { return []string{} }, nil, pool)
	router.now = func() time.Time {
		n = n.Add(time.Duration(table.TimeToLive) * time.Second * 2)
		return n
	}

	consumer := func() {
		for i := 0; i < 30; i++ {
			readers, err := router.Readers()
			if len(readers) != 2 {
				t.Error("Wrong number of readers")
			}
			if err != nil {
				t.Error(err)
			}
			writers, err := router.Writers()
			if len(writers) != 1 {
				t.Error("Wrong number of writers")
			}
			if err != nil {
				t.Error(err)
			}

		}
		wg.Done()
	}

	go consumer()
	go consumer()

	wg.Wait()
}

func assertNum(t *testing.T, x, y int, msg string) {
	t.Helper()
	if x != y {
		t.Error(msg)
	}
}

func TestRespectsTimeToLive(t *testing.T) {
	numfetch := 0
	table := &db.RoutingTable{TimeToLive: 1}
	pool := &poolFake{
		borrow: func(names []string, cancel context.CancelFunc) (poolpackage.Connection, error) {
			numfetch++
			return &connFake{table: table}, nil
		},
	}
	nzero := time.Now()
	n := nzero
	router := New("router", func() []string { return []string{} }, nil, pool)
	router.now = func() time.Time {
		return n
	}

	// First access should trigger initial table read
	router.Readers()
	assertNum(t, numfetch, 1, "Should have fetched initial")

	// Second access with time set to same should not trigger a read
	router.Readers()
	assertNum(t, numfetch, 1, "Should not have have fetched")

	// Third access with time passed table due should trigger fetch
	n = n.Add(2 * time.Second)
	router.Readers()
	assertNum(t, numfetch, 2, "Should have have fetched")
}

// Verify that when the routing table can not be retrieved from the root router, a callback
// should be invoked to get backup routers.
func TestUseGetRoutersHookWhenInitialRouterFails(t *testing.T) {
	tried := []string{}
	pool := &poolFake{
		borrow: func(names []string, cancel context.CancelFunc) (poolpackage.Connection, error) {
			tried = append(tried, names...)
			return nil, errors.New("fail")
		},
	}
	rootRouter := "rootRouter"
	backupRouters := []string{"bup1", "bup2"}
	router := New(rootRouter, func() []string { return backupRouters }, nil, pool)

	// Trigger read of routing table
	router.Readers()

	expected := []string{rootRouter}
	expected = append(expected, backupRouters...)

	if !reflect.DeepEqual(tried, expected) {
		t.Errorf("Didn't try the expected routers, tried: %#v", tried)
	}
}