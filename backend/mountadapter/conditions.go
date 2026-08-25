package mountadapter

import (
	"TDrive/backend/mountdav"
)

type conditionResource struct {
	exists bool
	etag   string
}

func opaqueETag(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return ""
}

func evaluateMutationConditions(
	conditions mountdav.MutationConditions,
	requestResource conditionResource,
	allowedResources map[string]conditionResource,
) error {
	if !matchesIfMatch(conditions.IfMatch, requestResource) ||
		!matchesIfNoneMatch(conditions.IfNoneMatch, requestResource) {
		return mountdav.ErrWritePreconditionFailed
	}
	if len(conditions.DAVIf) == 0 {
		return nil
	}

	lockTokens := make(map[string]struct{}, len(conditions.LockTokens))
	for _, token := range conditions.LockTokens {
		lockTokens[token] = struct{}{}
	}
	groupMatches := make(map[string]bool, len(conditions.DAVIf))
	for _, list := range conditions.DAVIf {
		resource, supported := allowedResources[list.ResourcePath]
		if !supported {
			return mountdav.ErrWritePreconditionFailed
		}
		if matchesDAVList(list, resource, lockTokens) {
			groupMatches[list.ResourcePath] = true
		} else if _, seen := groupMatches[list.ResourcePath]; !seen {
			groupMatches[list.ResourcePath] = false
		}
	}
	for _, matched := range groupMatches {
		if !matched {
			return mountdav.ErrWritePreconditionFailed
		}
	}
	return nil
}

func matchesIfMatch(condition mountdav.ETagConditions, resource conditionResource) bool {
	if !condition.Present {
		return true
	}
	if condition.Any {
		return resource.exists
	}
	if !resource.exists {
		return false
	}
	current := opaqueETag(resource.etag)
	for _, candidate := range condition.Tags {
		if !candidate.Weak && candidate.Opaque == current {
			return true
		}
	}
	return false
}

func matchesIfNoneMatch(condition mountdav.ETagConditions, resource conditionResource) bool {
	if !condition.Present || !resource.exists {
		return true
	}
	if condition.Any {
		return false
	}
	current := opaqueETag(resource.etag)
	for _, candidate := range condition.Tags {
		if candidate.Opaque == current {
			return false
		}
	}
	return true
}

func matchesDAVList(
	list mountdav.DAVConditionList,
	resource conditionResource,
	lockTokens map[string]struct{},
) bool {
	if len(list.Conditions) == 0 {
		return false
	}
	for _, condition := range list.Conditions {
		matched := false
		switch {
		case condition.ETag != nil:
			matched = resource.exists && !condition.ETag.Weak &&
				condition.ETag.Opaque == opaqueETag(resource.etag)
		case condition.LockToken != "":
			_, matched = lockTokens[condition.LockToken]
		}
		if condition.Not {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	return true
}
