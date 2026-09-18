package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// socketDeTeste devolve um caminho curto: sun_path aceita 107 bytes, e o
// t.TempDir() carrega o nome do teste inteiro no caminho.
func socketDeTeste(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "fiscal.sock")
}

// TestWorkerQueSaiNaoApagaOSocketDoSucessor reproduz a troca de worker num
// rolling update start-first (é o que o Swarm faz com serviço que tem
// healthcheck): o novo sobe, toma o caminho do socket, e só então o antigo
// recebe SIGTERM. Se o antigo apagar o caminho ao sair, o novo continua
// escutando num arquivo que não existe mais, e a API fica em 503 até alguém
// reiniciar o worker.
func TestWorkerQueSaiNaoApagaOSocketDoSucessor(t *testing.T) {
	caminho := socketDeTeste(t)

	antigo, err := escutar(caminho)
	if err != nil {
		t.Fatal(err)
	}
	novo, err := escutar(caminho)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = novo.Close() }()

	if err := antigo.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(caminho); err != nil {
		t.Fatalf("o worker que saiu apagou o socket do sucessor: %v", err)
	}
	conn, err := net.Dial("unix", caminho)
	if err != nil {
		t.Fatalf("o caminho existe mas ninguém atende nele: %v", err)
	}
	_ = conn.Close()
}

// TestWorkerQueSaiApagaOProprioSocket é a contraprova: sem sucessor, o caminho
// é deste processo, e sair sem apagá-lo deixaria um arquivo que a API tenta
// discar e recusa com "connection refused".
func TestWorkerQueSaiApagaOProprioSocket(t *testing.T) {
	caminho := socketDeTeste(t)

	lis, err := escutar(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if err := lis.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(caminho); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("o socket continuou no disco depois de o worker sair: %v", err)
	}
}
