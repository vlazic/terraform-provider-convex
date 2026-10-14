package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// fakeKeyAPI is an in-memory stand-in for the deploy key endpoints of the
// Management API, for both deployments and projects. It names created keys
// the way the live API did on 2026-10-14, "<name> (<uuid>)", and returns
// list entries in the live shape.
type fakeKeyAPI struct {
	mu sync.Mutex

	// keys holds each scope's keys, keyed by "deployments/<name>" or
	// "projects/<id>".
	keys    map[string][]convex.PlatformDeployKeyResponse
	secrets map[string]int64
	nextID  int64

	// listStatus, when set, is returned by every list call instead of a list.
	listStatus int

	// createDelay widens the window between a create and the list after it,
	// so unserialised parallel creates would see each other's keys.
	createDelay time.Duration

	deletes []string
}

func newFakeKeyAPI(t *testing.T) (*fakeKeyAPI, *convex.ClientWithResponses) {
	t.Helper()

	api := &fakeKeyAPI{
		keys:    map[string][]convex.PlatformDeployKeyResponse{},
		secrets: map[string]int64{},
		nextID:  100,
	}

	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	client, err := convex.NewAuthenticated(server.URL, "fake-token")

	if err != nil {
		t.Fatal(err)
	}

	return api, client
}

// seed adds an existing key to a scope.
func (f *fakeKeyAPI) seed(scope string, id int64, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.keys[scope] = append(f.keys[scope], convex.PlatformDeployKeyResponse{
		Id: id, Name: name, CreationTime: 1_760_400_000_000,
		AllowedActions: []convex.PlatformDeployKeyResponseAllowedActions{"deployment:deploy"},
	})
}

func (f *fakeKeyAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	cut := strings.LastIndex(trimmed, "/")

	if cut < 0 {
		http.NotFound(w, r)
		return
	}

	scope, op := trimmed[:cut], trimmed[cut+1:]
	preview := strings.HasPrefix(scope, "projects/")

	switch op {
	case "list_deploy_keys", "list_preview_deploy_keys":
		f.list(w, scope, preview)
	case "create_deploy_key", "create_preview_deploy_key":
		f.create(w, r, scope, preview)
	case "delete_deploy_key", "delete_preview_deploy_key":
		f.remove(w, r, scope)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeKeyAPI) list(w http.ResponseWriter, scope string, preview bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.listStatus != 0 {
		w.WriteHeader(f.listStatus)
		_, _ = w.Write([]byte(`{"code":"NotFound","message":"no such deployment"}`))
		return
	}

	// Rendered by hand rather than marshalled from the generated type, so the
	// test decodes the wire shape the live API returns (allowedOperations
	// included, explicit nulls) rather than a round trip of its own struct.
	entries := make([]string, 0, len(f.keys[scope]))

	for _, key := range f.keys[scope] {
		name, _ := json.Marshal(key.Name)
		entries = append(entries, fmt.Sprintf(`{"allowedActions":["deployment:deploy"],"allowedOperations":null,`+
			`"creationTime":%d,"creator":7,"expiresAt":null,"id":%d,"lastUsedTime":null,"name":%s}`,
			key.CreationTime, key.Id, name))
	}

	body := "[" + strings.Join(entries, ",") + "]"

	if preview {
		body = `{"items":` + body + `}`
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func (f *fakeKeyAPI) create(w http.ResponseWriter, r *http.Request, scope string, preview bool) {
	var args struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.nextID++
	id := f.nextID
	secret := fmt.Sprintf("secret-%d", id)
	f.secrets[secret] = id
	f.keys[scope] = append(f.keys[scope], convex.PlatformDeployKeyResponse{
		Id:           id,
		Name:         fmt.Sprintf("%s (%08x-0000-4000-8000-%012x)", args.Name, id, id),
		CreationTime: 1_760_400_000_000 + id,
	})
	delay := f.createDelay
	f.mu.Unlock()

	time.Sleep(delay)

	field := "deployKey"

	if preview {
		field = "previewDeployKey"
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{%q:%q}`, field, secret)
}

func (f *fakeKeyAPI) remove(w http.ResponseWriter, r *http.Request, scope string) {
	var args struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.deletes = append(f.deletes, args.ID)
	keys := f.keys[scope]

	for i := range keys {
		if keys[i].Name == args.ID || f.secrets[args.ID] == keys[i].Id {
			f.keys[scope] = append(keys[:i:i], keys[i+1:]...)
			_, _ = w.Write([]byte(`{}`))
			return
		}
	}

	http.Error(w, `{"code":"NotFound","message":"no such key"}`, http.StatusNotFound)
}

// schemaOf returns a resource's schema.
func schemaOf(t *testing.T, r resource.Resource) tfsdk.State {
	t.Helper()

	var resp resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics: %+v", resp.Diagnostics)
	}

	return tfsdk.State{
		Schema: resp.Schema,
		Raw:    tftypes.NewValue(resp.Schema.Type().TerraformType(t.Context()), nil),
	}
}

// stateOf builds a state (or, by conversion, a plan) holding model.
func stateOf(t *testing.T, r resource.Resource, model any) tfsdk.State {
	t.Helper()

	state := schemaOf(t, r)

	if diags := state.Set(t.Context(), model); diags.HasError() {
		t.Fatalf("State.Set() diagnostics: %+v", diags)
	}

	return state
}

func mustNoErrors(t *testing.T, diags diag.Diagnostics) {
	t.Helper()

	if diags.HasError() {
		t.Fatalf("unexpected error diagnostics: %+v", diags)
	}
}

const testDeployment = "playful-otter-123"

func deployKeyPlan(name string) deployKeyResourceModel {
	return deployKeyResourceModel{
		DeploymentName: types.StringValue(testDeployment),
		Name:           types.StringValue(name),
		AllowedActions: types.SetUnknown(types.StringType),
		ExpiresAt:      types.Int64Unknown(),
		Key:            types.StringUnknown(),
		UniqueName:     types.StringUnknown(),
		ID:             types.Int64Unknown(),
		CreationTime:   types.Int64Unknown(),
		LastUsedTime:   types.Int64Unknown(),
		Creator:        types.Int64Unknown(),
	}
}

// deployKeyState is state as a create that could not identify its key, or an
// import, leaves it: no id, and optionally a secret.
func deployKeyState(name, secret string) deployKeyResourceModel {
	m := deployKeyResourceModel{
		DeploymentName: types.StringValue(testDeployment),
		Name:           types.StringValue(name),
		AllowedActions: types.SetNull(types.StringType),
		ExpiresAt:      types.Int64Null(),
		Key:            types.StringNull(),
		UniqueName:     types.StringNull(),
		ID:             types.Int64Null(),
		CreationTime:   types.Int64Null(),
		LastUsedTime:   types.Int64Null(),
		Creator:        types.Int64Null(),
	}

	if secret != "" {
		m.Key = types.StringValue(secret)
	}

	return m
}

func createDeployKeyThroughResource(t *testing.T, r *deployKeyResource, name string) (deployKeyResourceModel, diag.Diagnostics) {
	t.Helper()

	plan := stateOf(t, r, ptr(deployKeyPlan(name)))
	resp := resource.CreateResponse{State: schemaOf(t, r)}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(plan)}, &resp)

	var got deployKeyResourceModel

	if !resp.State.Raw.IsNull() {
		mustNoErrors(t, resp.State.Get(t.Context(), &got))
	}

	return got, resp.Diagnostics
}

func readDeployKeyThroughResource(t *testing.T, r *deployKeyResource, state tfsdk.State) resource.ReadResponse {
	t.Helper()

	resp := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	return resp
}

func ptr[T any](v T) *T { return &v }

func TestDeployKeyCreatePicksTheNewKey(t *testing.T) {
	api, client := newFakeKeyAPI(t)
	scope := "deployments/" + testDeployment
	// A leftover from an earlier apply answers to the same name.
	api.seed(scope, 1, "ci ("+uuidA+")")

	r := &deployKeyResource{client: client}
	got, diags := createDeployKeyThroughResource(t, r, "ci")
	mustNoErrors(t, diags)

	if got.ID.ValueInt64() != 101 || got.Key.ValueString() != "secret-101" {
		t.Errorf("created id %v key %v, want 101 and secret-101", got.ID, got.Key)
	}

	// The configured name is kept; the listed one goes to unique_name.
	if got.Name.ValueString() != "ci" || !strings.HasPrefix(got.UniqueName.ValueString(), "ci (") {
		t.Errorf("name %v unique_name %v, want \"ci\" and the suffixed name", got.Name, got.UniqueName)
	}
}

func TestDeployKeyParallelSameNameCreatesBothResolve(t *testing.T) {
	api, client := newFakeKeyAPI(t)
	// Without the per-deployment lock, both "after" listings would see both
	// new keys.
	api.createDelay = 30 * time.Millisecond

	const parallel = 3

	var wg sync.WaitGroup
	results := make([]deployKeyResourceModel, parallel)
	failures := make([]diag.Diagnostics, parallel)

	for i := range parallel {
		wg.Add(1)

		go func() {
			defer wg.Done()
			// A fresh resource value each, as the framework may build.
			results[i], failures[i] = createDeployKeyThroughResource(t, &deployKeyResource{client: client}, "ci")
		}()
	}

	wg.Wait()

	seen := map[int64]bool{}

	for i := range parallel {
		mustNoErrors(t, failures[i])

		id := results[i].ID.ValueInt64()

		if seen[id] || results[i].ID.IsNull() {
			t.Errorf("create %d resolved to id %v, want a distinct id", i, results[i].ID)
		}

		seen[id] = true

		// Each must have identified the key its own secret belongs to.
		if want := fmt.Sprintf("secret-%d", id); results[i].Key.ValueString() != want {
			t.Errorf("create %d: id %d with secret %v, want %s", i, id, results[i].Key, want)
		}
	}
}

func TestDeployKeyReadNoIDWithSecretKeepsState(t *testing.T) {
	for name, seed := range map[string][]string{
		"missing":   nil,
		"ambiguous": {"ci (" + uuidA + ")", "ci (" + uuidB + ")"},
	} {
		t.Run(name, func(t *testing.T) {
			api, client := newFakeKeyAPI(t)

			for i, listed := range seed {
				api.seed("deployments/"+testDeployment, int64(i+1), listed)
			}

			r := &deployKeyResource{client: client}
			resp := readDeployKeyThroughResource(t, r, stateOf(t, r, ptr(deployKeyState("ci", "secret-9"))))
			mustNoErrors(t, resp.Diagnostics)

			if resp.State.Raw.IsNull() {
				t.Fatal("state was removed; a key whose secret is in state must be kept")
			}

			if resp.Diagnostics.WarningsCount() != 1 {
				t.Errorf("got %d warnings, want 1: %+v", resp.Diagnostics.WarningsCount(), resp.Diagnostics)
			}
		})
	}
}

func TestDeployKeyReadNoIDNoSecret(t *testing.T) {
	t.Run("missing is removed", func(t *testing.T) {
		_, client := newFakeKeyAPI(t)
		r := &deployKeyResource{client: client}
		resp := readDeployKeyThroughResource(t, r, stateOf(t, r, ptr(deployKeyState("ci", ""))))
		mustNoErrors(t, resp.Diagnostics)

		if !resp.State.Raw.IsNull() {
			t.Error("state was kept, want it removed")
		}
	})

	t.Run("ambiguous is an error", func(t *testing.T) {
		api, client := newFakeKeyAPI(t)
		api.seed("deployments/"+testDeployment, 1, "ci ("+uuidA+")")
		api.seed("deployments/"+testDeployment, 2, "ci ("+uuidB+")")

		r := &deployKeyResource{client: client}
		resp := readDeployKeyThroughResource(t, r, stateOf(t, r, ptr(deployKeyState("ci", ""))))

		if !resp.Diagnostics.HasError() {
			t.Fatal("want an error naming the candidates")
		}

		if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "id 1") ||
			!strings.Contains(detail, "id 2") {
			t.Errorf("error detail %q does not name both ids", detail)
		}
	})
}

func TestDeployKeyImportByUniqueName(t *testing.T) {
	for _, tt := range []struct {
		importName string
		wantID     int64
	}{
		// The legacy unsuffixed key, beside suffixed siblings.
		{"ci", 1},
		{"ci (" + uuidA + ")", 2},
	} {
		t.Run(tt.importName, func(t *testing.T) {
			api, client := newFakeKeyAPI(t)
			api.seed("deployments/"+testDeployment, 1, "ci")
			api.seed("deployments/"+testDeployment, 2, "ci ("+uuidA+")")
			api.seed("deployments/"+testDeployment, 3, "ci ("+uuidB+")")

			r := &deployKeyResource{client: client}
			imported := resource.ImportStateResponse{State: schemaOf(t, r)}
			r.ImportState(t.Context(), resource.ImportStateRequest{ID: testDeployment + "/" + tt.importName}, &imported)
			mustNoErrors(t, imported.Diagnostics)

			resp := readDeployKeyThroughResource(t, r, imported.State)
			mustNoErrors(t, resp.Diagnostics)

			var got deployKeyResourceModel
			mustNoErrors(t, resp.State.Get(t.Context(), &got))

			if got.ID.ValueInt64() != tt.wantID {
				t.Errorf("adopted id %v, want %d", got.ID, tt.wantID)
			}

			// Recorded as the short name, so a config using it plans no change.
			if got.Name.ValueString() != "ci" || got.UniqueName.ValueString() != tt.importName {
				t.Errorf("name %v unique_name %v, want \"ci\" and %q", got.Name, got.UniqueName, tt.importName)
			}
		})
	}
}

func TestDeployKeyDeleteByUniqueName(t *testing.T) {
	api, client := newFakeKeyAPI(t)
	r := &deployKeyResource{client: client}

	created, diags := createDeployKeyThroughResource(t, r, "ci")
	mustNoErrors(t, diags)

	resp := resource.DeleteResponse{State: stateOf(t, r, &created)}
	r.Delete(t.Context(), resource.DeleteRequest{State: resp.State}, &resp)
	mustNoErrors(t, resp.Diagnostics)

	// The listed name, not the configured one, and not the secret.
	if len(api.deletes) != 1 || api.deletes[0] != created.UniqueName.ValueString() {
		t.Errorf("deletes = %q, want [%q]", api.deletes, created.UniqueName.ValueString())
	}

	if len(api.keys["deployments/"+testDeployment]) != 0 {
		t.Error("the key is still listed")
	}
}

func TestDeployKeyDeleteListNotFoundIsGone(t *testing.T) {
	api, client := newFakeKeyAPI(t)
	api.listStatus = http.StatusNotFound

	r := &deployKeyResource{client: client}
	state := deployKeyState("ci", "")
	state.ID = types.Int64Value(4)

	resp := resource.DeleteResponse{State: stateOf(t, r, &state)}
	r.Delete(t.Context(), resource.DeleteRequest{State: resp.State}, &resp)
	mustNoErrors(t, resp.Diagnostics)

	if len(api.deletes) != 0 {
		t.Errorf("deletes = %q, want none", api.deletes)
	}
}

// Preview keys are listed inside an {"items": [...]} wrapper.
func TestPreviewDeployKeyCreateAndRead(t *testing.T) {
	api, client := newFakeKeyAPI(t)
	api.seed("projects/12", 1, "previews ("+uuidA+")")

	r := &previewDeployKeyResource{client: client}
	plan := stateOf(t, r, &previewDeployKeyResourceModel{
		ProjectID:      types.Int64Value(12),
		Name:           types.StringValue("previews"),
		ExpiresAt:      types.Int64Unknown(),
		Key:            types.StringUnknown(),
		UniqueName:     types.StringUnknown(),
		ID:             types.Int64Unknown(),
		CreationTime:   types.Int64Unknown(),
		LastUsedTime:   types.Int64Unknown(),
		Creator:        types.Int64Unknown(),
		AllowedActions: types.SetUnknown(types.StringType),
	})

	created := resource.CreateResponse{State: schemaOf(t, r)}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(plan)}, &created)
	mustNoErrors(t, created.Diagnostics)

	var got previewDeployKeyResourceModel
	mustNoErrors(t, created.State.Get(t.Context(), &got))

	if got.ID.ValueInt64() != 101 || got.Key.ValueString() != "secret-101" || got.Name.ValueString() != "previews" {
		t.Errorf("created id %v key %v name %v, want 101, secret-101, previews", got.ID, got.Key, got.Name)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	mustNoErrors(t, read.Diagnostics)

	var after previewDeployKeyResourceModel
	mustNoErrors(t, read.State.Get(t.Context(), &after))

	if after.ID.ValueInt64() != 101 || after.CreationTime.ValueInt64() != 1_760_400_000_101 {
		t.Errorf("read id %v creation_time %v, want 101 and 1760400000101", after.ID, after.CreationTime)
	}
}
