package main

// main_skills_workshop.go — control-RPC bridges for the skills.curator.* and
// skills.proposals.* long-tail methods (swarmstr-xfny.3/.4). These adapt decoded
// gateway params to the internal/skills curator + proposal stores.

import (
	"metiq/internal/gateway/methods"
	skillspkg "metiq/internal/skills"
)

func proposalSupportInputs(files []methods.SkillProposalSupportFile) []skillspkg.ProposalFileInput {
	if len(files) == 0 {
		return nil
	}
	out := make([]skillspkg.ProposalFileInput, 0, len(files))
	for _, f := range files {
		out = append(out, skillspkg.ProposalFileInput{Path: f.Path, Content: f.Content})
	}
	return out
}

func proposalCreateInput(req methods.SkillsProposalsCreateRequest) skillspkg.ProposalDraftInput {
	return skillspkg.ProposalDraftInput{
		Title:           req.Title,
		Description:     req.Description,
		Content:         req.Content,
		ProposedVersion: req.ProposedVersion,
		SkillName:       req.SkillName,
		SkillKey:        req.SkillKey,
		SupportFiles:    proposalSupportInputs(req.SupportFiles),
	}
}

func proposalReviseInput(req methods.SkillsProposalsReviseRequest) skillspkg.ProposalDraftInput {
	return skillspkg.ProposalDraftInput{
		Title:           req.Title,
		Description:     req.Description,
		Content:         req.Content,
		ProposedVersion: req.ProposedVersion,
		SupportFiles:    proposalSupportInputs(req.SupportFiles),
	}
}
