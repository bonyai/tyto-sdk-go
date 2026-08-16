// Command previews publishes a guest port at an HTTPS URL a browser can open.
//
//	export BONYA_API_KEY=byk_...
//	go run ./examples/previews
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	tyto "github.com/bonyai/tyto-go"
)

func main() {
	apiKey := os.Getenv("BONYA_API_KEY")

	client, err := tyto.NewClient(tyto.WithAPIKey(apiKey))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	sandbox, err := client.CreateSandbox(ctx, "ubuntu-24.04")
	if err != nil {
		log.Fatal(err)
	}
	defer sandbox.Delete(ctx)

	if _, err := client.CreateSession(ctx, sandbox.ID, "web", []string{"python3", "-m", "http.server", "3000"}); err != nil {
		log.Fatal(err)
	}

	// Ports must be 1024-65535; privileged ports are never previewable.
	// PreviewAuthToken is the default, and an omitted Auth never yields a
	// public URL.
	preview, err := client.CreatePreview(ctx, sandbox.ID, 3000, tyto.CreatePreviewOptions{Name: "web"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("preview: %s\n", preview.URL)

	// A token-mode URL needs the sandbox's capability, and a URL is not a safe
	// place to leave one. BrowserURL mints a single-use entry point: the
	// gateway validates the token, swaps it for an HttpOnly cookie, and
	// redirects to the same address without it.
	//
	// Open it once and let the cookie carry the session. Do not share it --
	// whoever holds it holds the sandbox's capability.
	browserURL, err := sandbox.Previews.BrowserURL(preview)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("open once: %s\n", browserURL)

	previews, err := client.ListPreviews(ctx, sandbox.ID)
	if err != nil {
		log.Fatal(err)
	}
	for _, existing := range previews {
		fmt.Printf("%s :%d %s\n", existing.ID, existing.Port, existing.Auth)
	}

	// PreviewAuthPublic means exactly that: no credential at all.
	public, err := client.CreatePreview(ctx, sandbox.ID, 8080, tyto.CreatePreviewOptions{Auth: tyto.PreviewAuthPublic})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("public (anyone with this URL): %s\n", public.URL)

	if err := client.DeletePreview(ctx, sandbox.ID, preview.ID); err != nil {
		log.Fatal(err)
	}
	if err := client.DeletePreview(ctx, sandbox.ID, public.ID); err != nil {
		log.Fatal(err)
	}
}
