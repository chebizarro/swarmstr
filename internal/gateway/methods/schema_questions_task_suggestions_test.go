package methods

import (
	"encoding/json"
	"testing"
)

func TestDecodeQuestionRequestParamsNormalizes(t *testing.T) {
	raw := `{"questions":[{"questionId":"approach","header":"Approach","question":"Which?","options":[{"label":"A"},{"label":"B"}]}],"agentId":"main","sessionKey":"sess","timeoutMs":5000}`
	req, err := DecodeQuestionRequestParams(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.AgentID != "main" || req.SessionKey != "sess" || req.TimeoutMS != 5000 {
		t.Fatalf("unexpected: %+v", req)
	}
	if len(req.Questions) != 1 || req.Questions[0].QuestionID != "approach" {
		t.Fatalf("unexpected questions: %+v", req.Questions)
	}
}

func TestQuestionRequestValidationRules(t *testing.T) {
	base := func() QuestionRequestRequest {
		return QuestionRequestRequest{Questions: []QuestionInputParam{{
			QuestionID: "approach",
			Header:     "Approach",
			Question:   "Which?",
			Options:    []QuestionOptionParam{{Label: "A"}, {Label: "B"}},
		}}}
	}

	if _, err := (QuestionRequestRequest{}).Normalize(); err == nil {
		t.Fatal("expected error for empty questions")
	}

	tooMany := base()
	for _, id := range []string{"second", "third", "fourth"} {
		q := tooMany.Questions[0]
		q.QuestionID = id
		tooMany.Questions = append(tooMany.Questions, q)
	}
	if _, err := tooMany.Normalize(); err == nil {
		t.Fatal("expected error for more than 3 questions")
	}

	badID := base()
	badID.Questions[0].QuestionID = "BadID"
	if _, err := badID.Normalize(); err == nil {
		t.Fatal("expected error for invalid question id")
	}

	dup := base()
	dup.Questions = append(dup.Questions, dup.Questions[0])
	if _, err := dup.Normalize(); err == nil {
		t.Fatal("expected error for duplicate question id")
	}

	longHeader := base()
	longHeader.Questions[0].Header = "ThisHeaderIsTooLong"
	if _, err := longHeader.Normalize(); err == nil {
		t.Fatal("expected error for long header")
	}

	oneOption := base()
	oneOption.Questions[0].Options = oneOption.Questions[0].Options[:1]
	if _, err := oneOption.Normalize(); err == nil {
		t.Fatal("expected error for a single option")
	}

	secret := base()
	secret.Questions[0].IsSecret = true
	if _, err := secret.Normalize(); err == nil {
		t.Fatal("expected error for secret question")
	}

	dupLabel := base()
	dupLabel.Questions[0].Options = []QuestionOptionParam{{Label: "A"}, {Label: " a "}}
	if _, err := dupLabel.Normalize(); err == nil {
		t.Fatal("expected error for duplicate option labels")
	}
}

func TestQuestionResolveParamsUnion(t *testing.T) {
	answered, err := DecodeQuestionResolveParams(json.RawMessage(`{"id":"q1","answers":{"answers":{"approach":["A"]}},"resolvedBy":"op"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answered, err = answered.Normalize(); err != nil || answered.Answers.Answers["approach"][0] != "A" || answered.ResolvedBy != "op" {
		t.Fatalf("unexpected: %+v err=%v", answered, err)
	}

	cancelled, err := DecodeQuestionResolveParams(json.RawMessage(`{"id":"q1","cancel":true}`))
	if err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if _, err = cancelled.Normalize(); err != nil {
		t.Fatalf("normalize cancel: %v", err)
	}

	neither, _ := DecodeQuestionResolveParams(json.RawMessage(`{"id":"q1"}`))
	if _, err := neither.Normalize(); err == nil {
		t.Fatal("expected error when neither cancel nor answers present")
	}

	both, _ := DecodeQuestionResolveParams(json.RawMessage(`{"id":"q1","cancel":true,"answers":{"answers":{}}}`))
	if _, err := both.Normalize(); err == nil {
		t.Fatal("expected error when cancel and answers are combined")
	}
}

func TestQuestionWaitAnswerAndGetRequireID(t *testing.T) {
	if _, err := (QuestionWaitAnswerRequest{}).Normalize(); err == nil {
		t.Fatal("waitAnswer must require id")
	}
	if _, err := (QuestionGetRequest{}).Normalize(); err == nil {
		t.Fatal("get must require id")
	}
	req, err := DecodeQuestionWaitAnswerParams(json.RawMessage(`{"id":"q1","timeoutMs":250}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.TimeoutMS != 250 {
		t.Fatalf("unexpected: %+v err=%v", req, err)
	}
}

func TestDecodeTaskSuggestionsCreateParams(t *testing.T) {
	raw := `{"title":"Follow up","prompt":"Do it","tldr":"short","cwd":"/tmp/project","sessionKey":"sess","agentId":"main"}`
	req, err := DecodeTaskSuggestionsCreateParams(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.AgentID != "main" || req.CWD != "/tmp/project" {
		t.Fatalf("unexpected: %+v err=%v", req, err)
	}
}

func TestTaskSuggestionsCreateValidationRules(t *testing.T) {
	base := TaskSuggestionsCreateRequest{
		Title: "t", Prompt: "p", Tldr: "s", CWD: "/tmp", SessionKey: "sess",
	}

	relative := base
	relative.CWD = "relative/path"
	if _, err := relative.Normalize(); err == nil {
		t.Fatal("expected error for relative cwd")
	}

	missingTitle := base
	missingTitle.Title = ""
	if _, err := missingTitle.Normalize(); err == nil {
		t.Fatal("expected error for missing title")
	}

	missingSession := base
	missingSession.SessionKey = ""
	if _, err := missingSession.Normalize(); err == nil {
		t.Fatal("expected error for missing sessionKey")
	}
}

func TestTaskSuggestionsAcceptDismissParams(t *testing.T) {
	accept, err := DecodeTaskSuggestionsAcceptParams(json.RawMessage(`{"taskId":"task_1"}`))
	if err != nil {
		t.Fatalf("decode accept: %v", err)
	}
	if accept, err = accept.Normalize(); err != nil || accept.TaskID != "task_1" {
		t.Fatalf("unexpected: %+v err=%v", accept, err)
	}
	if _, err := (TaskSuggestionsAcceptRequest{}).Normalize(); err == nil {
		t.Fatal("accept must require task_id")
	}

	dismiss, err := DecodeTaskSuggestionsDismissParams(json.RawMessage(`{"taskId":"task_1","reason":"stale"}`))
	if err != nil {
		t.Fatalf("decode dismiss: %v", err)
	}
	if dismiss, err = dismiss.Normalize(); err != nil || dismiss.Reason != "stale" {
		t.Fatalf("unexpected: %+v err=%v", dismiss, err)
	}
}

func TestQuestionAndTaskSuggestionDescriptors(t *testing.T) {
	cases := map[string]string{
		MethodQuestionRequest:        "operator.questions",
		MethodQuestionWaitAnswer:     "operator.questions",
		MethodQuestionResolve:        "operator.questions",
		MethodQuestionGet:            "operator.questions",
		MethodQuestionList:           "operator.questions",
		MethodTaskSuggestionsList:    "operator.read",
		MethodTaskSuggestionsCreate:  "operator.write",
		MethodTaskSuggestionsAccept:  "operator.admin",
		MethodTaskSuggestionsDismiss: "operator.write",
	}
	supported := map[string]struct{}{}
	for _, method := range SupportedMethods() {
		supported[method] = struct{}{}
	}
	for method, wantScope := range cases {
		if _, ok := supported[method]; !ok {
			t.Fatalf("method %s is not in SupportedMethods", method)
		}
		if got := MethodDescriptor(method).Scope; got != wantScope {
			t.Fatalf("method %s scope = %s, want %s", method, got, wantScope)
		}
	}
}
