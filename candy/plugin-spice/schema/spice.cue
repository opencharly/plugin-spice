// The `spice` plugin's OWN CUE schema — the typed plugin_input for the `spice`
// SPICE-wire check verb. It is the SINGLE SOURCE for this plugin's params, used
// two ways (the same contract core `spec` and the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by the cue:gen
//     pipeline, which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a
//     TYPED struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `spice:` step's plugin_input against #SpiceInput.
//
// Since the schema-compaction cutover the per-verb fields LEFT core #Op: an
// authored `spice: <method>` step (scalar sugar) or `spice: {method: …, x: …}`
// (map form) desugars to the INTERNAL plugin/plugin_input envelope, and every
// spice-exclusive modifier lives HERE — the former core #SpiceMethod enum is
// this def's `method` field. The shared assertion matchers
// (exit_status/stdout/stderr) and the general `timeout` stay on core #Op, read
// off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone
// (gengotypes + the load-gate compile) AND splices onto the base (base ++ plugin
// is a def-name collision check, not a base-reference resolver).
#SpiceInput: {
	// method — the spice method to dispatch (the former core #SpiceMethod enum;
	// also the scalar-sugar primary: `spice: <method>`).
	method: "status" | "screenshot" | "cursor" | "click" | "mouse" | "type" | "key" | "record" | "session"
	// action — start|stop for a record session (record); start|stop|status for a
	// session (session). record start begins capturing the display framebuffer at
	// fps into an MJPEG stream; record stop flushes it to artifact.
	action?: "start" | "stop" | "status" @go(Action)
	// record_name — the recording session name (default "default"); multiple
	// concurrent sessions supported.
	record_name?: string @go(RecordName)
	// fps — the display-frame capture rate for record (default 5).
	fps?: int & >=1 @go(Fps,type=int)
	// x / y — guest-absolute coordinates (click/mouse).
	x?: int @go(,type=int)
	y?: int @go(,type=int)
	// button — the mouse button for click (left/right/middle; default left).
	button?: string
	// text — the text `type` types (PC-AT scancode sequence).
	text?: string
	// key — the named key `key` presses.
	key?: string @go(KeyName)
	// artifact — the host path `screenshot`/`cursor` writes the PNG to.
	artifact?: string
	// artifact_min_bytes / artifact_min_dimensions / artifact_not_uniform — the
	// post-run artifact-reality assertions (sdk.RunArtifactValidators).
	artifact_min_bytes?:      int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?: string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:    bool                         @go(ArtifactNotUniform)
	// session — the DETACHED host-side recorder (Cutover A, A-task-2b): `spice: session` starts
	// the plugin's OWN binary in recorder mode through the runner's generic
	// background-session service (plugin-check's verb:session seam). The recorder
	// holds the SPICE wire itself — the provider stays wire-free — polls the display
	// at fps into state_dir/frames.mjpeg, and on SIGTERM finalizes with the FINAL
	// marker + the evidence row.json. venue/phase are stamped into the evidence row.
	session_id?: string @go(SessionId)
	state_dir?:  string @go(StateDir)
	// artifact_dir — the runner-injected generic evidence-artifact dir (verb-agnostic;
	// the provider appends its own filename/extension).
	artifact_dir?: string @go(ArtifactDir)
	log_dir?:  string @go(LogDir)
	venue?:      string @go(Venue)
	phase?:      string @go(Phase)
}
