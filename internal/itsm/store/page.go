package store

import "strconv"

type PageParams struct {
	Page     int
	PageSize int
}

type PageResult[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

func DefaultPageParams() PageParams {
	return PageParams{Page: 1, PageSize: 20}
}

func ClampPage(p PageParams) PageParams {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
	return p
}

func ParsePageParams(q interface{ Get(string) string }) PageParams {
	p := DefaultPageParams()
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.Page = n
		}
	}
	if v := q.Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.PageSize = n
		}
	}
	return ClampPage(p)
}

func (p PageParams) Offset() int {
	return (p.Page - 1) * p.PageSize
}
