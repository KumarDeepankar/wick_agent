#!/usr/bin/env python3
"""
Analytical MCP Server - Tool 2: analyze_all_events

A FastMCP server that provides:
- Keyword and date field filtering with fuzzy matching
- Full-text search fallback when keyword filters don't match
- Strong guardrails for consistent results
- Rich contextual responses with full metadata

This tool queries across all indices matching the pattern 'events_*'.
Uses event_date as the primary date field.
"""
import os
import json
import logging
from typing import Optional, List, Dict, Any
from fastmcp.tools.tool import ToolResult
from rapidfuzz import fuzz

from text_search import text_search_with_filters
from query_classifier import classify_search_text
from document_merge import get_merged_documents_batch
from pagination import create_pit, delete_pit, parse_search_after, apply_pagination_to_search, build_pagination_metadata

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

# ============================================================================
# CONFIGURATION
# ============================================================================

# Index pattern - queries across ALL event indices
INDEX_NAME = os.getenv("INDEX_NAME_ALL", "events_*")

# Field configuration

# Unique identifier field for deduplication (treats multiple docs with same ID as one record)
UNIQUE_ID_FIELD = os.getenv("UNIQUE_ID_FIELD", "rid")

KEYWORD_FIELDS = os.getenv(
    "KEYWORD_FIELDS",
    "country,event_title,event_theme,rid,docid,url"
).split(",")

# Fields that support fuzzy search via .fuzzy sub-field (multi-field mapping)
FUZZY_SEARCH_FIELDS = os.getenv(
    "FUZZY_SEARCH_FIELDS",
    "country,event_title,event_theme"
).split(",")

# Fields that support word search via .words sub-field (standard analyzer)
WORD_SEARCH_FIELDS = os.getenv(
    "WORD_SEARCH_FIELDS",
    "event_title"
).split(",")

# Use event_date as the date field for this tool
DATE_FIELDS = os.getenv("DATE_FIELDS_TOOL2", "event_date").split(",")

# Derived fields - extracted from date fields at query time (no physical field in index)
# Maps derived field name -> source date field
DERIVED_YEAR_FIELDS = {
    "year": "event_date"  # year extracted from event_date
}

# All filterable fields (includes derived fields)
ALL_FILTER_FIELDS = KEYWORD_FIELDS + DATE_FIELDS + list(DERIVED_YEAR_FIELDS.keys())

# Result fields to return (includes event_date)
RESULT_FIELDS = os.getenv(
    "RESULT_FIELDS_TOOL2",
    "rid,docid,event_title,event_theme,country,event_date,url"
).split(",")

# Valid date histogram intervals
VALID_DATE_INTERVALS = ["year", "quarter", "month", "week", "day"]

# Document return configuration
MAX_DOCUMENTS = 10

# Samples per bucket configuration - sample docs returned inside each aggregation bucket
SAMPLES_PER_BUCKET_DEFAULT = int(os.getenv("SAMPLES_PER_BUCKET_DEFAULT", "0"))  # 0 = disabled

# Verbose data context - include index-wide stats in response
VERBOSE_DATA_CONTEXT = os.getenv("VERBOSE_DATA_CONTEXT", "true").lower() == "true"

# Field descriptions for agent context (can be overridden via FIELD_DESCRIPTIONS env var as JSON)
DEFAULT_FIELD_DESCRIPTIONS = {
    "country": "Geographic location where the event took place",
    "event_title": "Name/title of the event",
    "event_theme": "Topic or category of the event",
    "event_date": "Date when the event occurred",
    "year": "Year extracted from event_date (derived field)",
    "rid": "Unique record identifier",
    "docid": "Document identifier",
    "url": "Source URL of the event"
}

# Load custom descriptions from env if provided, otherwise use defaults
_custom_desc = os.getenv("FIELD_DESCRIPTIONS", "")
if _custom_desc:
    try:
        FIELD_DESCRIPTIONS = {**DEFAULT_FIELD_DESCRIPTIONS, **json.loads(_custom_desc)}
    except json.JSONDecodeError:
        FIELD_DESCRIPTIONS = DEFAULT_FIELD_DESCRIPTIONS
else:
    FIELD_DESCRIPTIONS = DEFAULT_FIELD_DESCRIPTIONS


# ============================================================================
# DYNAMIC DOCSTRING
# ============================================================================

ANALYTICS_DOCSTRING = f"""Events analytics tool (all indices). Query with filters and/or aggregations.

<fields>
keyword: {', '.join(KEYWORD_FIELDS)}
date: event_date
year: integer (derived from event_date)
</fields>

<parameters>
filters: JSON string - exact match '{{"country": "India", "year": 2023}}' (PREFERRED - use when field is known)
range_filters: JSON string - range '{{"year": {{"gte": 2020, "lte": 2024}}}}'
fallback_search: str - LAST RESORT when field unknown. Auto-classifies to filters or text search.
group_by: str - single "country" or nested "country,year"
date_histogram: JSON string - '{{"field": "event_date", "interval": "year|quarter|month|week|day"}}'
top_n: int - max buckets (default 20)
top_n_per_group: int - nested buckets (default 5)
samples_per_bucket: int - sample docs per bucket (default 0)
page_size: int - docs per page (default 10, max 100) for pagination
search_after: str - JSON array of sort values from previous response's pagination.search_after
pit_id: str - PIT ID from previous response's pagination.pit_id (reuses existing PIT)
</parameters>

<date_formats>
"2023" → full year (gte: Jan 1, lt: Jan 1 next year)
"Q1 2023" or "2023-Q1" or "2023Q1" → quarter (gte: Jan 1, lt: Apr 1)
"2023-06" → month (gte: Jun 1, lt: Jul 1)
"2023-06-15" → exact date (no expansion)
Note: Uses "lt" (less than) with next period start to correctly include all timestamps within the period.
</date_formats>

<examples>
Top countries: group_by="country", top_n=10
Country by theme: group_by="country,event_theme", top_n=5, top_n_per_group=3
Yearly trend: date_histogram='{{"field": "event_date", "interval": "year"}}'
Quarterly trend: date_histogram='{{"field": "event_date", "interval": "quarter"}}'
Monthly trend: date_histogram='{{"field": "event_date", "interval": "month"}}'
Weekly trend: date_histogram='{{"field": "event_date", "interval": "week"}}'
Daily trend: date_histogram='{{"field": "event_date", "interval": "day"}}'

Filter by country: filters='{{"country": "India"}}'
Filter by year: filters='{{"year": 2023}}'
Filter + group: filters='{{"country": "India"}}', group_by="event_theme"
Filter by year + group: filters='{{"year": 2023}}', group_by="country"
Multi-filter + group: filters='{{"country": "India", "year": 2023}}', group_by="event_theme"

Range + group: range_filters='{{"year": {{"gte": 2020, "lte": 2024}}}}', group_by="country"
Range + trend: range_filters='{{"year": {{"gte": 2020}}}}', date_histogram='{{"field": "event_date", "interval": "year"}}'

Filter by full year: filters='{{"event_date": "2023"}}', group_by="country"
Filter by quarter (Q1 2023): filters='{{"event_date": "Q1 2023"}}', group_by="event_theme"
Filter by quarter (2023-Q1): filters='{{"event_date": "2023-Q1"}}', group_by="event_theme"
Filter by month: filters='{{"event_date": "2023-06"}}', group_by="country"
Filter by exact date: filters='{{"event_date": "2023-06-15"}}', group_by="country"

Samples per bucket: group_by="country", samples_per_bucket=3
Filter + samples: filters='{{"year": 2023}}', group_by="country", samples_per_bucket=2

Fallback search + group: fallback_search="tech summit", group_by="country"
Fallback search + filter: fallback_search="annual conference", filters='{{"year": 2023}}'

Pagination (first page): filters='{{"country": "India"}}', page_size=50
Pagination (next page): filters='{{"country": "India"}}', page_size=50, search_after='["RID050"]'
Pagination (with PIT): filters='{{"country": "India"}}', page_size=50, search_after='["RID100"]', pit_id="abc..."
</examples>

<rules>
1. ALWAYS use filters + group_by/date_histogram when fields are known
2. fallback_search is LAST RESORT - only when user query cannot be mapped to a field
3. Provide at least one: filters OR group_by OR date_histogram OR fallback_search
</rules>

<response>
status: "success" | "empty_query" | "no_results"
mode: "filter_only" | "aggregation" | "search"
aggregations: group_by buckets, date_histogram
documents: matching documents
pagination: {{total_hits, search_after, pit_id, has_more, page_size}} - use search_after + pit_id for next page
</response>
"""


# ============================================================================
# FUZZY MATCHING VIA OPENSEARCH
# ============================================================================

async def resolve_keyword_filter(
    field: str,
    value: str,
    use_fuzzy: bool = True
) -> Dict[str, Any]:
    """
    Resolve a keyword filter value using OpenSearch.

    Strategy:
    1. Try exact match on keyword field
    2. Try prefix match (case-insensitive) on keyword field
    3. Try contains/substring match (wildcard) on keyword field
    4. Try fuzzy match on .fuzzy field
       (handles case-insensitive + whitespace normalization via normalized_fuzzy analyzer)
    5. Return match metadata for transparency

    Returns:
        {
            "match_type": "exact" | "prefix" | "contains" | "approximate" | "none",
            "query_value": original value,
            "matched_values": [list of matched values],
            "filter_clause": OpenSearch filter clause or None,
            "confidence": 100 for exact, <100 for fuzzy
        }
    """
    import shared_state
    opensearch_request = shared_state.opensearch_request
    # Use module's INDEX_NAME (not shared_state)

    # Step 1: Check exact match exists
    exact_query = {
        "size": 0,
        "query": {"term": {field: value}},
        "aggs": {"check": {"terms": {"field": field, "size": 1}}}
    }

    try:
        result = await opensearch_request("POST", f"{INDEX_NAME}/_search", exact_query)
        hits = result.get("hits", {}).get("total", {}).get("value", 0)

        if hits > 0:
            # Exact match found
            return {
                "match_type": "exact",
                "query_value": value,
                "matched_values": [value],
                "filter_clause": {"term": {field: value}},
                "confidence": 100,
                "hit_count": hits
            }
    except Exception as e:
        logger.warning(f"Exact match query failed: {e}")

    # Step 1.5: Try prefix match on keyword field (case-insensitive)
    if field in FUZZY_SEARCH_FIELDS:
        prefix_query = {
            "size": 0,
            "query": {"prefix": {field: {"value": value, "case_insensitive": True}}},
            "aggs": {"matched_values": {"terms": {"field": field, "size": 10}}}
        }
        try:
            result = await opensearch_request("POST", f"{INDEX_NAME}/_search", prefix_query)
            hits = result.get("hits", {}).get("total", {}).get("value", 0)
            buckets = result.get("aggregations", {}).get("matched_values", {}).get("buckets", [])

            if hits > 0 and buckets:
                matched_values = [b["key"] for b in buckets]
                if len(matched_values) == 1:
                    filter_clause = {"term": {field: matched_values[0]}}
                else:
                    filter_clause = {"terms": {field: matched_values}}

                return {
                    "match_type": "prefix",
                    "query_value": value,
                    "matched_values": matched_values,
                    "filter_clause": filter_clause,
                    "confidence": 95,
                    "hit_count": hits,
                    "warning": f"Prefix match: '{value}' matched to {matched_values}"
                }
        except Exception as e:
            logger.warning(f"Prefix match query failed: {e}")

    # Step 1.6: Try contains/substring match via wildcard (case-insensitive)
    if field in FUZZY_SEARCH_FIELDS:
        wildcard_query = {
            "size": 0,
            "query": {"wildcard": {field: {"value": f"*{value}*", "case_insensitive": True}}},
            "aggs": {"matched_values": {"terms": {"field": field, "size": 10}}}
        }
        try:
            result = await opensearch_request("POST", f"{INDEX_NAME}/_search", wildcard_query)
            hits = result.get("hits", {}).get("total", {}).get("value", 0)
            buckets = result.get("aggregations", {}).get("matched_values", {}).get("buckets", [])

            if hits > 0 and buckets:
                matched_values = [b["key"] for b in buckets]
                if len(matched_values) == 1:
                    filter_clause = {"term": {field: matched_values[0]}}
                else:
                    filter_clause = {"terms": {field: matched_values}}

                return {
                    "match_type": "contains",
                    "query_value": value,
                    "matched_values": matched_values,
                    "filter_clause": filter_clause,
                    "confidence": 85,
                    "hit_count": hits,
                    "warning": f"Contains match: '{value}' matched to {matched_values}"
                }
        except Exception as e:
            logger.warning(f"Contains match query failed: {e}")

    # Step 2: Try fuzzy match on .fuzzy field and word match on .words field
    # The normalized_fuzzy analyzer handles case-insensitive + whitespace normalization
    if use_fuzzy and field in FUZZY_SEARCH_FIELDS:
        search_field = f"{field}.fuzzy"

        # Build query - use bool.should to combine fuzzy and word match
        should_clauses = [
            {
                "match": {
                    search_field: {
                        "query": value,
                        "fuzziness": "AUTO",
                        "prefix_length": 1
                    }
                }
            }
        ]

        # Add word match for fields that support it (no fuzziness - exact word match)
        if field in WORD_SEARCH_FIELDS:
            should_clauses.append({
                "match": {
                    f"{field}.words": {
                        "query": value
                    }
                }
            })

        fuzzy_query = {
            "size": 0,
            "query": {
                "bool": {
                    "should": should_clauses,
                    "minimum_should_match": 1
                }
            },
            "aggs": {
                "matched_values": {
                    "terms": {"field": field, "size": 10}
                }
            }
        }

        try:
            result = await opensearch_request("POST", f"{INDEX_NAME}/_search", fuzzy_query)
            hits = result.get("hits", {}).get("total", {}).get("value", 0)
            buckets = result.get("aggregations", {}).get("matched_values", {}).get("buckets", [])

            if hits > 0 and buckets:
                matched_values = [b["key"] for b in buckets]

                # Calculate confidence based on string similarity
                best_match = matched_values[0]
                confidence = fuzz.ratio(value.lower(), str(best_match).lower())

                # Build filter for all matched values
                if len(matched_values) == 1:
                    filter_clause = {"term": {field: matched_values[0]}}
                else:
                    filter_clause = {"terms": {field: matched_values}}

                return {
                    "match_type": "approximate",
                    "query_value": value,
                    "matched_values": matched_values,
                    "filter_clause": filter_clause,
                    "confidence": round(confidence, 1),
                    "hit_count": hits,
                    "warning": f"Fuzzy match: '{value}' matched to {matched_values}"
                }
        except Exception as e:
            logger.warning(f"Fuzzy match query failed: {e}")

    # No match found
    return {
        "match_type": "none",
        "query_value": value,
        "matched_values": [],
        "filter_clause": None,
        "confidence": 0,
        "hit_count": 0
    }


# ============================================================================
# MAIN ANALYTICS TOOL
# ============================================================================

async def analyze_all_events(
    filters: Optional[str] = None,
    range_filters: Optional[str] = None,
    fallback_search: Optional[str] = None,
    group_by: Optional[str] = None,
    date_histogram: Optional[str] = None,
    top_n: int = 20,
    top_n_per_group: int = 5,
    samples_per_bucket: int = SAMPLES_PER_BUCKET_DEFAULT,
    page_size: int = MAX_DOCUMENTS,
    search_after: Optional[str] = None,
    pit_id: Optional[str] = None
) -> ToolResult:
    """
    Analyze events with filtering, grouping, and aggregations.
    All inputs are validated and normalized with fuzzy matching.
    Requires at least one: filter OR aggregation (group_by/date_histogram).
    """
    # Import from shared_state to avoid circular import issues
    import shared_state

    if shared_state.validator_tool2 is None:
        return ToolResult(content=[], structured_content={
            "error": "Server not initialized. Please wait and retry."
        })

    # Local references for cleaner code (use tool2-specific instances)
    validator = shared_state.validator_tool2
    metadata = shared_state.metadata_tool2
    opensearch_request = shared_state.opensearch_request
    # Use module's INDEX_NAME (defined at top of file)

    warnings: List[str] = []
    match_metadata: Dict[str, Any] = {}  # Track match types for transparency
    query_context: Dict[str, Any] = {
        "filters_applied": {},
        "filters_normalized": {},
        "range_filters_applied": {},
        "aggregations": []
    }

    # ===== PARSE JSON PARAMETERS =====

    parsed_filters = {}
    if filters:
        try:
            parsed_filters = json.loads(filters)
        except json.JSONDecodeError as e:
            return ToolResult(content=[], structured_content={
                "error": f"Invalid filters JSON: {e}"
            })

    # ===== PARSE & VALIDATE PAGINATION PARAMS =====

    # Clamp page_size to [1, 100]
    page_size = max(1, min(100, page_size))

    # Parse search_after
    parsed_search_after = None
    if search_after:
        try:
            parsed_search_after = parse_search_after(search_after)
        except ValueError as e:
            return ToolResult(content=[], structured_content={
                "error": str(e)
            })

    # Auto-create PIT when search_after is provided without pit_id
    active_pit_id = pit_id
    if parsed_search_after and not active_pit_id:
        try:
            active_pit_id = await create_pit(opensearch_request, INDEX_NAME)
        except Exception as e:
            return ToolResult(content=[], structured_content={
                "error": f"Failed to create PIT for pagination: {str(e)}"
            })

    logger.info(f"Pagination params: page_size={page_size}, search_after={'set' if parsed_search_after else 'none'}, pit_id={'set' if active_pit_id else 'none'}")

    # ===== CLASSIFY FALLBACK_SEARCH (if provided) =====
    classification_result = None
    fallback_unclassified_terms = []

    if fallback_search and fallback_search.strip():
        logger.info(f"Processing fallback_search: '{fallback_search}'")

        classification_result = await classify_search_text(
            search_text=fallback_search,
            keyword_fields=KEYWORD_FIELDS,
            word_search_fields=WORD_SEARCH_FIELDS,
            fuzzy_search_fields=FUZZY_SEARCH_FIELDS,
            opensearch_request=opensearch_request,
            index_name=INDEX_NAME
        )

        # Merge classified filters (explicit filters take precedence)
        for field, value in classification_result.classified_filters.items():
            if field not in parsed_filters:
                parsed_filters[field] = value
                warnings.append(f"Auto-classified '{field}' = '{value}' from fallback_search")

        # Store unclassified terms for text search fallback
        fallback_unclassified_terms = classification_result.unclassified_terms

        # Add classification details to query context
        query_context["fallback_search"] = {
            "original_query": fallback_search,
            "classified_filters": classification_result.classified_filters,
            "unclassified_terms": classification_result.unclassified_terms,
            "classification_details": classification_result.classification_details
        }

        warnings.extend(classification_result.warnings)

    parsed_range_filters = {}
    if range_filters:
        try:
            parsed_range_filters = json.loads(range_filters)
        except json.JSONDecodeError as e:
            return ToolResult(content=[], structured_content={
                "error": f"Invalid range_filters JSON: {e}"
            })

    parsed_date_histogram = None
    if date_histogram:
        try:
            parsed_date_histogram = json.loads(date_histogram)
            if "field" not in parsed_date_histogram:
                return ToolResult(content=[], structured_content={
                    "error": "date_histogram requires 'field' parameter"
                })
            if parsed_date_histogram["field"] not in DATE_FIELDS:
                return ToolResult(content=[], structured_content={
                    "error": f"Invalid date_histogram field. Valid: {', '.join(DATE_FIELDS)}"
                })
            if "interval" not in parsed_date_histogram:
                parsed_date_histogram["interval"] = "month"
            elif parsed_date_histogram["interval"] not in VALID_DATE_INTERVALS:
                return ToolResult(content=[], structured_content={
                    "error": f"Invalid interval. Valid: {', '.join(VALID_DATE_INTERVALS)}"
                })
        except json.JSONDecodeError as e:
            return ToolResult(content=[], structured_content={
                "error": f"Invalid date_histogram JSON: {e}"
            })


    # ===== VALIDATE AND NORMALIZE FILTERS =====

    filter_clauses = []
    search_terms = []  # Values that failed keyword matching (will become text search)

    for field, value in parsed_filters.items():
        # Validate field name
        field_result = validator.validate_field_name(field, ALL_FILTER_FIELDS)
        if not field_result.valid:
            return ToolResult(content=[], structured_content={
                "error": f"Unknown filter field '{field}'",
                "suggestions": field_result.suggestions
            })
        field = field_result.normalized_value

        # Validate value based on field type
        if field in KEYWORD_FIELDS:
            # Handle list values (e.g., {"country": ["India", "Brazil"]})
            if isinstance(value, list):
                if not value:  # Empty list
                    warnings.append(f"Empty list for '{field}' - skipping filter")
                    continue

                all_matched_values = []
                all_query_values = []
                match_types = []
                confidences = []
                list_warnings = []
                failed_values = []

                for v in value:
                    v_str = str(v)
                    all_query_values.append(v_str)
                    resolve_result = await resolve_keyword_filter(field, v_str)

                    if resolve_result["match_type"] == "none":
                        failed_values.append(v_str)
                    else:
                        all_matched_values.extend(resolve_result["matched_values"])
                        match_types.append(resolve_result["match_type"])
                        confidences.append(resolve_result["confidence"])
                        if resolve_result["match_type"] == "approximate":
                            list_warnings.append(resolve_result.get("warning", f"Approximate match for '{v_str}'"))

                if not all_matched_values:
                    # All values failed - add to search_terms for text search fallback
                    search_terms.extend([str(v) for v in value])
                    warnings.append(f"No matches for any value in '{field}' list - will use text search")
                    match_metadata[field] = {
                        "match_type": "search_fallback",
                        "query_value": value,
                        "matched_values": [],
                        "confidence": 0
                    }
                    continue

                # Some or all values matched
                if failed_values:
                    # Partial match - warn but continue with matched values only
                    # Don't add to search_terms since list is an OR condition
                    warnings.append(f"Partial match for '{field}': {failed_values} not found in index (ignored)")

                warnings.extend(list_warnings)

                # Deduplicate matched values while preserving order
                unique_matched = list(dict.fromkeys(all_matched_values))

                # Determine overall match type
                overall_match_type = "exact" if all(mt == "exact" for mt in match_types) else "approximate"
                avg_confidence = sum(confidences) / len(confidences) if confidences else 0

                match_metadata[field] = {
                    "match_type": overall_match_type,
                    "query_value": value,
                    "matched_values": unique_matched,
                    "confidence": round(avg_confidence, 1)
                }

                query_context["filters_normalized"][field] = {
                    "original": value,
                    "matched": unique_matched,
                    "match_type": overall_match_type,
                    "confidence": round(avg_confidence, 1)
                }

                # Build terms filter clause
                if len(unique_matched) == 1:
                    filter_clauses.append({"term": {field: unique_matched[0]}})
                else:
                    filter_clauses.append({"terms": {field: unique_matched}})

            else:
                # Single value (existing behavior)
                resolve_result = await resolve_keyword_filter(field, str(value))

                if resolve_result["match_type"] == "none":
                    # No match found - add to search_terms for text search fallback
                    search_terms.append(str(value))
                    warnings.append(f"No exact match for '{value}' in '{field}' - will use text search")

                    # Store metadata about the failed filter
                    match_metadata[field] = {
                        "match_type": "search_fallback",
                        "query_value": value,
                        "matched_values": [],
                        "confidence": 0
                    }
                    continue

                # Store match metadata for transparency
                match_metadata[field] = {
                    "match_type": resolve_result["match_type"],
                    "query_value": resolve_result["query_value"],
                    "matched_values": resolve_result["matched_values"],
                    "confidence": resolve_result["confidence"]
                }

                # Add warning if approximate match
                if resolve_result["match_type"] == "approximate":
                    warnings.append(resolve_result.get("warning", f"Approximate match for {field}"))

                if resolve_result.get("note"):
                    warnings.append(resolve_result["note"])

                query_context["filters_normalized"][field] = {
                    "original": value,
                    "matched": resolve_result["matched_values"],
                    "match_type": resolve_result["match_type"],
                    "confidence": resolve_result["confidence"]
                }

                filter_clauses.append(resolve_result["filter_clause"])

        elif field in DATE_FIELDS:
            result = validator.validate_date(field, str(value))
            if not result.valid:
                return ToolResult(content=[], structured_content={
                    "error": result.warnings[0] if result.warnings else f"Invalid date: {value}",
                    "suggestions": result.suggestions
                })
            warnings.extend(result.warnings)

            # If date expanded to range, use range filter
            if result.field_type == "date_range":
                filter_clauses.append({"range": {field: result.normalized_value}})
                query_context["filters_normalized"][field] = {
                    "original": value,
                    "expanded_to": result.normalized_value
                }
            else:
                filter_clauses.append({"term": {field: result.normalized_value}})

        elif field in DERIVED_YEAR_FIELDS:
            # Derived year field - convert to date range on source date field
            source_date_field = DERIVED_YEAR_FIELDS[field]
            try:
                year_val = int(value)
                # Convert year to date range: year 2023 -> gte: 2023-01-01, lt: 2024-01-01
                date_range = {
                    "gte": f"{year_val}-01-01",
                    "lt": f"{year_val + 1}-01-01"
                }
                filter_clauses.append({"range": {source_date_field: date_range}})
                query_context["filters_normalized"][field] = {
                    "original": value,
                    "derived_from": source_date_field,
                    "expanded_to": date_range
                }
            except (ValueError, TypeError):
                return ToolResult(content=[], structured_content={
                    "error": f"Invalid year value '{value}'. Expected integer (e.g., 2023)"
                })

        query_context["filters_applied"][field] = value

    # Add unclassified terms from fallback_search to search_terms
    if fallback_unclassified_terms:
        search_terms.extend(fallback_unclassified_terms)
        logger.info(f"Adding unclassified terms to text search: {fallback_unclassified_terms}")

    # ===== VALIDATE AND NORMALIZE RANGE FILTERS =====

    for field, range_spec in parsed_range_filters.items():
        # Validate field name (includes derived year fields)
        valid_range_fields = DATE_FIELDS + list(DERIVED_YEAR_FIELDS.keys())
        field_result = validator.validate_field_name(field, valid_range_fields)
        if not field_result.valid:
            return ToolResult(content=[], structured_content={
                "error": f"Range filter not supported for '{field}'",
                "suggestions": field_result.suggestions
            })
        field = field_result.normalized_value

        if field in DERIVED_YEAR_FIELDS:
            # Derived year field - convert year range to date range
            source_date_field = DERIVED_YEAR_FIELDS[field]
            date_range = {}
            try:
                if "gte" in range_spec:
                    year_val = int(range_spec["gte"])
                    date_range["gte"] = f"{year_val}-01-01"
                if "gt" in range_spec:
                    year_val = int(range_spec["gt"])
                    date_range["gte"] = f"{year_val + 1}-01-01"  # gt 2022 means >= 2023
                if "lte" in range_spec:
                    year_val = int(range_spec["lte"])
                    date_range["lt"] = f"{year_val + 1}-01-01"  # lte 2023 means < 2024
                if "lt" in range_spec:
                    year_val = int(range_spec["lt"])
                    date_range["lt"] = f"{year_val}-01-01"
            except (ValueError, TypeError) as e:
                return ToolResult(content=[], structured_content={
                    "error": f"Invalid year range value. Expected integers (e.g., {{'gte': 2020, 'lte': 2024}})"
                })
            filter_clauses.append({"range": {source_date_field: date_range}})
            query_context["range_filters_applied"][field] = {
                "original": range_spec,
                "derived_from": source_date_field,
                "expanded_to": date_range
            }
        else:
            result = validator.validate_date_range(field, range_spec)
            if not result.valid:
                return ToolResult(content=[], structured_content={
                    "error": result.warnings[0] if result.warnings else "Invalid range filter",
                    "suggestions": result.suggestions
                })
            warnings.extend(result.warnings)
            filter_clauses.append({"range": {field: result.normalized_value}})
            query_context["range_filters_applied"][field] = result.normalized_value

    # ===== VALIDATE GROUP BY (supports multi-level: "country,year") =====

    group_by_fields = []
    if group_by:
        raw_fields = [f.strip() for f in group_by.split(",") if f.strip()]
        # Allow grouping by keyword and derived year fields
        valid_group_by_fields = KEYWORD_FIELDS + list(DERIVED_YEAR_FIELDS.keys())
        for gf in raw_fields:
            field_result = validator.validate_field_name(gf, valid_group_by_fields)
            if not field_result.valid:
                return ToolResult(content=[], structured_content={
                    "error": f"Cannot group by '{gf}'",
                    "suggestions": field_result.suggestions
                })
            group_by_fields.append(field_result.normalized_value)

        if len(group_by_fields) == 1:
            query_context["aggregations"].append(f"group_by:{group_by_fields[0]}")
        else:
            query_context["aggregations"].append(f"group_by:{' -> '.join(group_by_fields)}")

    if parsed_date_histogram:
        query_context["aggregations"].append(
            f"date_histogram:{parsed_date_histogram['field']}:{parsed_date_histogram['interval']}"
        )

    # ===== REQUIRE AT LEAST ONE: FILTER OR AGGREGATION =====

    has_filters = bool(filter_clauses) or bool(search_terms)  # Include search_terms as they came from filters
    has_aggregation = bool(group_by_fields or parsed_date_histogram)
    has_pagination = bool(parsed_search_after) or (page_size != MAX_DOCUMENTS)

    if not has_filters and not has_aggregation and not has_pagination:
        return ToolResult(content=[], structured_content={
            "status": "empty_query",
            "error": "Query is empty - specify filter or aggregation",
            "message": "Provide at least one: filters OR fallback_search OR group_by OR date_histogram",
            "examples": {
                "filter_only": {"filters": "{\"country\": \"India\"}"},
                "group_by_country": {"group_by": "country"},
                "group_by_theme": {"group_by": "event_theme"},
                "monthly_trend": {"date_histogram": "{\"field\": \"event_date\", \"interval\": \"month\"}"},
                "filter_and_group": {"filters": "{\"country\": \"India\"}", "group_by": "event_theme"},
                "fallback_search_with_group": {"fallback_search": "tech summit", "group_by": "country"}
            },
            "available_fields": {
                "filters": ALL_FILTER_FIELDS,
                "group_by": KEYWORD_FIELDS + list(DERIVED_YEAR_FIELDS.keys()),
                "date_histogram": DATE_FIELDS
            }
        })

    # Flag for filter-only mode (no aggregation)
    filter_only_mode = has_filters and not has_aggregation

    # ===== TEXT SEARCH FALLBACK =====
    # If any keyword filter failed (no match), use text search with remaining filters

    if search_terms:
        logger.info(f"Text search fallback: terms={search_terms}, filters={len(filter_clauses)}")

        search_result = await text_search_with_filters(
            search_terms=search_terms,
            filter_clauses=filter_clauses,
            opensearch_request=opensearch_request,
            index_name=INDEX_NAME,
            unique_id_field=UNIQUE_ID_FIELD,
            max_results=page_size,
            source_fields=RESULT_FIELDS,
            pit_id=active_pit_id,
            search_after=parsed_search_after
        )

        if search_result["status"] == "success":
            # Merge documents for text search results
            search_docs = search_result["documents"]
            if search_docs:
                rids = [doc.get(UNIQUE_ID_FIELD) for doc in search_docs if doc.get(UNIQUE_ID_FIELD)]
                if rids:
                    search_docs = await get_merged_documents_batch(
                        unique_ids=rids,
                        opensearch_request=opensearch_request,
                        index_name=INDEX_NAME,
                        unique_id_field=UNIQUE_ID_FIELD,
                        source_fields=RESULT_FIELDS
                    )

            # Build data context for text search
            search_data_context = {
                "unique_ids_matched": search_result["unique_hits"]
            }
            if VERBOSE_DATA_CONTEXT:
                search_data_context.update({
                    "unique_id_field": UNIQUE_ID_FIELD,
                    "total_unique_ids_in_index": metadata.total_unique_ids,
                    "total_documents_in_index": metadata.total_documents,
                    "search_query": search_result["search_query"],
                    "search_terms": search_terms,
                    "documents_matched": search_result["total_hits"],
                    "max_score": search_result["max_score"],
                    "fields_searched": search_result["fields_searched"],
                    "filters_applied": {f: v for f, v in parsed_filters.items() if any(f in c.get("term", {}) or f in c.get("range", {}) for c in filter_clauses)} if filter_clauses else {}
                })

            return ToolResult(content=[], structured_content={
                "status": "success",
                "mode": "search",
                "data_context": search_data_context,
                "documents": search_docs,
                "warnings": warnings,
                "match_metadata": match_metadata,
                "pagination": search_result.get("pagination")
            })
        else:
            # Text search also returned no results
            return ToolResult(content=[], structured_content={
                "status": "no_results",
                "mode": "search",
                "data_context": {
                    "search_query": " ".join(search_terms),
                    "search_terms": search_terms,
                    "fields_searched": search_result["fields_searched"]
                },
                "error": f"No results found for search: '{' '.join(search_terms)}'",
                "warnings": warnings,
                "match_metadata": match_metadata,
                "pagination": search_result.get("pagination")
            })

    # ===== BUILD OPENSEARCH QUERY =====

    query_body = {"match_all": {}} if not filter_clauses else {
        "bool": {"filter": filter_clauses}
    }

    # Document fields to return
    doc_fields = RESULT_FIELDS

    # Skip top-level docs when samples_per_bucket is enabled with group_by (avoid redundancy)
    top_level_doc_size = 0 if (samples_per_bucket > 0 and group_by_fields) else page_size

    search_body: Dict[str, Any] = {
        "query": query_body,
        "size": top_level_doc_size,
        "track_total_hits": True,
        "aggs": {
            # Always count unique IDs for accurate totals
            "unique_ids": {"cardinality": {"field": UNIQUE_ID_FIELD, "precision_threshold": 40000}}
        },
        "_source": doc_fields
    }

    # Add field collapse to deduplicate documents by unique ID field
    if top_level_doc_size > 0:
        search_body["collapse"] = {"field": UNIQUE_ID_FIELD}

    # Add group by aggregation (supports multi-level nesting)
    if group_by_fields:
        def build_nested_agg(fields: List[str], depth: int = 0) -> Dict[str, Any]:
            """Recursively build nested terms aggregations with unique ID counts."""
            field = fields[0]
            size = top_n if depth == 0 else top_n_per_group

            # Check if this is a derived year field
            if field in DERIVED_YEAR_FIELDS:
                source_date_field = DERIVED_YEAR_FIELDS[field]
                # Use script-based aggregation to extract year from date field
                agg: Dict[str, Any] = {
                    "terms": {
                        "script": {
                            "source": f"if (doc['{source_date_field}'].size() == 0) return null; doc['{source_date_field}'].value.year",
                            "lang": "painless"
                        },
                        "missing_bucket": True,
                        "size": size
                    },
                    "aggs": {
                        "unique_ids": {"cardinality": {"field": UNIQUE_ID_FIELD, "precision_threshold": 40000}}
                    }
                }
            else:
                agg: Dict[str, Any] = {
                    "terms": {"field": field, "size": size},
                    "aggs": {
                        # Count unique IDs in each bucket for accurate counts
                        "unique_ids": {"cardinality": {"field": UNIQUE_ID_FIELD, "precision_threshold": 40000}}
                    }
                }

            # Add samples at the deepest level if samples_per_bucket > 0
            # Use terms on unique ID field with top_hits to deduplicate samples
            if len(fields) == 1 and samples_per_bucket > 0:
                agg["aggs"]["unique_samples"] = {
                    "terms": {
                        "field": UNIQUE_ID_FIELD,
                        "size": samples_per_bucket
                    },
                    "aggs": {
                        "sample_doc": {
                            "top_hits": {
                                "size": 1,
                                "_source": doc_fields
                            }
                        }
                    }
                }

            # Recurse for nested levels
            if len(fields) > 1:
                nested_agg = build_nested_agg(fields[1:], depth + 1)
                agg["aggs"]["nested"] = nested_agg

            return agg

        search_body["aggs"]["group_by_agg"] = build_nested_agg(group_by_fields)

    # Add date histogram
    if parsed_date_histogram:
        field = parsed_date_histogram["field"]
        interval = parsed_date_histogram["interval"]

        format_map = {
            "year": "yyyy",
            "quarter": "yyyy-QQQ",
            "month": "yyyy-MM",
            "week": "yyyy-'W'ww",
            "day": "yyyy-MM-dd"
        }

        search_body["aggs"]["date_histogram_agg"] = {
            "date_histogram": {
                "field": field,
                "calendar_interval": interval,
                "format": format_map.get(interval, "yyyy-MM-dd"),
                "min_doc_count": 0,
                "order": {"_key": "asc"},
                "time_zone": "UTC"
            },
            "aggs": {
                # Count unique IDs per time bucket
                "unique_ids": {"cardinality": {"field": UNIQUE_ID_FIELD, "precision_threshold": 40000}}
            }
        }

    # Auto-add aggregations for filter-only mode to get accurate chart data
    auto_agg_fields = []
    if filter_only_mode:
        # Select fields for auto-aggregation (exclude fields already filtered on)
        filtered_fields = set(parsed_filters.keys())
        MAX_AUTO_AGGS = 2  # Generate aggregations for up to 2 fields

        for field in KEYWORD_FIELDS:
            if field not in filtered_fields and field not in ["rid", "docid", "url"]:  # Skip ID/URL fields
                auto_agg_fields.append(field)
                search_body["aggs"][f"auto_agg_{field}"] = {
                    "terms": {"field": field, "size": 10},
                    "aggs": {
                        "unique_ids": {"cardinality": {"field": UNIQUE_ID_FIELD, "precision_threshold": 40000}}
                    }
                }
                if len(auto_agg_fields) >= MAX_AUTO_AGGS:
                    break

    # ===== ADD DETERMINISTIC SORT + PIT PAGINATION =====

    # Always add deterministic sort for consistent pagination
    search_body["sort"] = [{UNIQUE_ID_FIELD: {"order": "asc"}}]

    # Apply PIT-based pagination if active
    search_url = f"{INDEX_NAME}/_search"
    if active_pit_id:
        apply_pagination_to_search(search_body, active_pit_id, parsed_search_after)
        search_url = "_search"

    # ===== EXECUTE SEARCH =====

    try:
        data = await opensearch_request("POST", search_url, search_body)
    except Exception as e:
        return ToolResult(content=[], structured_content={
            "error": f"Search failed: {str(e)}"
        })

    # ===== BUILD RESPONSE =====

    total_hits = data.get("hits", {}).get("total", {}).get("value", 0)
    aggs = data.get("aggregations", {})

    # Use unique ID count for accurate totals (treats duplicate ID docs as one)
    total_unique_ids = aggs.get("unique_ids", {}).get("value", 0)
    total_matched = total_unique_ids if total_unique_ids > 0 else total_hits

    # Data context - verbose by default (VERBOSE_DATA_CONTEXT=true)
    data_context = {
        "unique_ids_matched": total_matched
    }

    if VERBOSE_DATA_CONTEXT:
        data_context.update({
            "index_pattern": INDEX_NAME,
            "unique_id_field": UNIQUE_ID_FIELD,
            "total_unique_ids_in_index": metadata.total_unique_ids,
            "total_documents_in_index": metadata.total_documents,
            "documents_matched": total_hits,
            "match_percentage": round(
                (total_matched / metadata.total_unique_ids * 100)
                if metadata.total_unique_ids > 0 else 0,
                2
            ),
            "date_range": {
                field: {
                    "min": metadata.date_ranges.get(field).min if metadata.date_ranges.get(field) else None,
                    "max": metadata.date_ranges.get(field).max if metadata.date_ranges.get(field) else None
                }
                for field in DATE_FIELDS
            }
        })

    # Extract documents from response
    hits = data.get("hits", {}).get("hits", [])

    # Build pagination metadata from raw hits
    pagination = build_pagination_metadata(hits, total_hits, active_pit_id, page_size)

    # Auto-cleanup PIT when no more pages (prevents resource leak)
    if active_pit_id and not pagination.get("has_more", True):
        await delete_pit(opensearch_request, active_pit_id)
        pagination["pit_id"] = None  # Signal PIT is closed

    collapsed_documents = [h["_source"] for h in hits]

    # Merge all documents per RID (combines duplicates into single document)
    if collapsed_documents:
        rids = [doc.get(UNIQUE_ID_FIELD) for doc in collapsed_documents if doc.get(UNIQUE_ID_FIELD)]
        if rids:
            documents = await get_merged_documents_batch(
                unique_ids=rids,
                opensearch_request=opensearch_request,
                index_name=INDEX_NAME,
                unique_id_field=UNIQUE_ID_FIELD,
                source_fields=RESULT_FIELDS
            )
        else:
            documents = collapsed_documents
    else:
        documents = []

    # Aggregation results
    aggregations: Dict[str, Any] = {}

    # Group by results (supports multi-level)
    if group_by_fields and "group_by_agg" in aggs:

        async def extract_nested_buckets(agg_data: dict, fields: List[str], depth: int = 0) -> List[dict]:
            """Recursively extract nested aggregation buckets with unique ID counts."""
            results = []
            buckets = agg_data.get("buckets", [])

            for b in buckets:
                # Use unique_ids count instead of doc_count for accurate counting
                unique_count = b.get("unique_ids", {}).get("value", b["doc_count"])

                item = {
                    "key": b["key"],
                    "count": unique_count,  # Unique ID count
                    "doc_count": b["doc_count"],  # Raw doc count for reference
                    "percentage": round(
                        unique_count / total_matched * 100
                        if total_matched > 0 else 0,
                        1
                    )
                }

                # Add samples if present (at deepest level) - fetch and merge docs per unique ID
                if "unique_samples" in b:
                    # Collect unique IDs from sample buckets
                    sample_ids = [id_bucket["key"] for id_bucket in b["unique_samples"].get("buckets", [])]
                    if sample_ids:
                        # Fetch and merge all docs for each unique ID
                        samples_list = await get_merged_documents_batch(
                            unique_ids=sample_ids,
                            opensearch_request=opensearch_request,
                            index_name=INDEX_NAME,
                            unique_id_field=UNIQUE_ID_FIELD,
                            source_fields=RESULT_FIELDS
                        )
                    else:
                        samples_list = []

                    item["samples"] = samples_list
                    item["samples_returned"] = len(samples_list)
                    item["other_ids_in_bucket"] = unique_count - len(samples_list)

                # Recurse into nested aggregation
                if "nested" in b and len(fields) > 1:
                    item["sub_groups"] = {
                        "field": fields[1],
                        "buckets": await extract_nested_buckets(b["nested"], fields[1:], depth + 1)
                    }

                results.append(item)

            return results

        group_results = await extract_nested_buckets(aggs["group_by_agg"], group_by_fields)

        # Calculate other count based on unique RIDs
        top_n_count = sum(r["count"] for r in group_results)
        other_count = total_matched - top_n_count

        aggregations["group_by"] = {
            "fields": group_by_fields,
            "multi_level": len(group_by_fields) > 1,
            "buckets": group_results,
            "total_groups": len(group_results),
            "other_count": max(0, other_count)
        }

    # Date histogram results
    if parsed_date_histogram and "date_histogram_agg" in aggs:
        buckets = aggs["date_histogram_agg"].get("buckets", [])
        aggregations["date_histogram"] = {
            "field": parsed_date_histogram["field"],
            "interval": parsed_date_histogram["interval"],
            "buckets": [
                {
                    "date": b.get("key_as_string", b.get("key")),
                    "count": b.get("unique_ids", {}).get("value", b["doc_count"]),  # Unique ID count
                    "doc_count": b["doc_count"],  # Raw doc count for reference
                    "percentage": round(
                        b.get("unique_ids", {}).get("value", b["doc_count"]) / total_matched * 100
                        if total_matched > 0 else 0,
                        1
                    )
                }
                for b in buckets
            ]
        }
        # Add note if bucket sum differs from total (OpenSearch cardinality approximation)
        try:
            bucket_sum = sum(b.get("unique_ids", {}).get("value", b["doc_count"]) for b in buckets)
            if bucket_sum != total_matched and total_matched > 0:
                aggregations["date_histogram"]["note"] = (
                    f"Bucket sum ({bucket_sum}) differs from total ({total_matched}) "
                    "due to OpenSearch cardinality approximation"
                )
        except Exception:
            pass  # Silently skip note if calculation fails


    # Extract auto-aggregation results for filter-only mode
    auto_aggregations = {}
    if filter_only_mode and auto_agg_fields:
        for field in auto_agg_fields:
            agg_key = f"auto_agg_{field}"
            if agg_key in aggs:
                buckets = aggs[agg_key].get("buckets", [])
                if buckets:
                    auto_aggregations[field] = {
                        "field": field,
                        "buckets": [
                            {
                                "key": b["key"],
                                "count": b.get("unique_ids", {}).get("value", b["doc_count"]),
                                "doc_count": b["doc_count"]
                            }
                            for b in buckets
                        ]
                    }

    # Generate chart config
    # Pass auto_aggregations for filter-only mode to get accurate server-side counts
    chart_config = _generate_chart_config(
        aggregations,
        group_by_fields,
        parsed_date_histogram,
        auto_aggregations=auto_aggregations if filter_only_mode else None,
        filters_applied=query_context["filters_applied"]
    )

    # Build filter_resolution - clear summary of what was actually searched
    filter_resolution = {}
    for field, meta in match_metadata.items():
        matched = meta.get("matched_values", [])
        if meta.get("match_type") == "exact":
            filter_resolution[field] = {
                "searched": matched[0] if len(matched) == 1 else matched,
                "exact_match": True
            }
        else:
            filter_resolution[field] = {
                "you_searched": meta.get("query_value"),
                "closest_match": matched[0] if matched else None,
                "searched": matched[0] if len(matched) == 1 else matched,
                "exact_match": False,
                "confidence": meta.get("confidence")
            }

    # Check if all matches are exact (None if no filters applied)
    all_exact = all(
        m.get("match_type") == "exact"
        for m in match_metadata.values()
    ) if match_metadata else None

    # Build final response
    response = {
        "status": "success",
        "mode": "filter_only" if filter_only_mode else "aggregation",
        "filters_used": filter_resolution,
        "exact_match": all_exact,
        "query_context": query_context,
        "data_context": data_context,
        "aggregations": aggregations if not filter_only_mode else auto_aggregations,  # Include auto-aggregations for filter-only
        "warnings": warnings,
        "chart_config": chart_config,  # Always include chart_config
        "pagination": pagination
    }

    # Add documents to response
    response["documents"] = documents
    response["document_count"] = len(documents)

    return ToolResult(content=[], structured_content=response)


# ============================================================================
# HELPER FUNCTIONS
# ============================================================================

def _generate_chart_config(
    aggregations: Dict[str, Any],
    group_by_fields: Optional[List[str]],
    date_histogram: Optional[dict],
    auto_aggregations: Optional[Dict[str, Any]] = None,
    filters_applied: Optional[Dict[str, Any]] = None
) -> List[dict]:
    """
    Generate chart configuration from aggregation results.

    Smart chart type selection:
    - Pie/Doughnut: <= 5 categories with significant distribution
    - Horizontal Bar: > 8 categories (easier to read)
    - Stacked Bar: Multi-level group by fields
    - Area: Time series data
    - Bar: Default for categorical data

    For filter-only mode, uses auto_aggregations (server-side aggregations) for accurate counts.
    Includes filters_applied for chart display context.
    """
    charts = []

    def select_chart_type(bucket_count: int, is_multi_level: bool = False, is_time_series: bool = False) -> str:
        """Select optimal chart type based on data characteristics."""
        if is_time_series:
            return "area"  # Area charts work great for time series
        if is_multi_level:
            return "stackedBar"  # Stacked for multi-level grouping
        if bucket_count <= 5:
            return "doughnut"  # Doughnut for small category counts
        if bucket_count > 8:
            return "horizontalBar"  # Horizontal bar for many categories
        return "bar"  # Default to vertical bar

    # Group by chart (top level only for multi-level)
    if group_by_fields and "group_by" in aggregations:
        group_data = aggregations["group_by"]
        buckets = group_data.get("buckets", [])
        if buckets:
            field_name = group_by_fields[0]
            is_multi_level = len(group_by_fields) > 1
            title_suffix = ""
            if is_multi_level:
                title_suffix = f" (with {', '.join(group_by_fields[1:])} breakdown)"

            chart_type = select_chart_type(len(buckets), is_multi_level=is_multi_level)

            charts.append({
                "type": chart_type,
                "title": f"Events by {field_name.replace('_', ' ').title()}{title_suffix}",
                "labels": [str(b["key"]) for b in buckets],
                "data": [b["count"] for b in buckets],
                "aggregation_field": field_name,
                "multi_level": is_multi_level,
                "total_records": sum(b["count"] for b in buckets),
                "filters": filters_applied or {}
            })

    # Date histogram chart - use area for time series
    if date_histogram and "date_histogram" in aggregations:
        hist_data = aggregations["date_histogram"]
        buckets = hist_data.get("buckets", [])
        if buckets:
            interval = hist_data.get("interval", "month")
            charts.append({
                "type": "area",  # Area chart for time series
                "title": f"Events Over Time (by {interval})",
                "labels": [str(b["date"]) for b in buckets],
                "data": [b["count"] for b in buckets],
                "aggregation_field": "date_histogram",
                "interval": interval,
                "total_records": sum(b["count"] for b in buckets),
                "filters": filters_applied or {}
            })

    # Filter-only mode: generate charts from auto-aggregations (server-side, accurate counts)
    if not charts and auto_aggregations:
        for field, agg_data in auto_aggregations.items():
            buckets = agg_data.get("buckets", [])
            if buckets:
                chart_type = select_chart_type(len(buckets))
                charts.append({
                    "type": chart_type,
                    "title": f"Distribution by {field.replace('_', ' ').title()}",
                    "labels": [str(b["key"]) for b in buckets],
                    "data": [b["count"] for b in buckets],
                    "aggregation_field": field,
                    "source": "auto_aggregation",  # Server-side aggregation
                    "total_records": sum(b["count"] for b in buckets),
                    "filters": filters_applied or {}
                })

    return charts


# ============================================================================
# FIELD CONTEXT BUILDER (Self-contained for this module)
# ============================================================================

def build_dynamic_field_context() -> str:
    """
    Build field context from loaded metadata for this tool.
    Returns a formatted string with field descriptions, valid values, and ranges.

    This is self-contained - uses only this module's field configurations.
    """
    import shared_state

    if shared_state.metadata_tool2 is None:
        return "Field context not available - server not initialized"

    metadata = shared_state.metadata_tool2
    max_samples = shared_state.FIELD_CONTEXT_MAX_SAMPLES

    lines = []

    # Keyword fields with descriptions and sample values
    lines.append("Keyword Fields:")
    for field in KEYWORD_FIELDS:
        desc = FIELD_DESCRIPTIONS.get(field, "")
        count = len(metadata.get_keyword_values(field))
        top_vals = metadata.get_keyword_top_values(field, max_samples)
        samples = [str(v["value"]) for v in top_vals]
        if desc:
            lines.append(f"  {field}: {desc}")
            lines.append(f"    - {count} unique values, e.g., {samples}")
        else:
            lines.append(f"  {field}: {count} unique values, e.g., {samples}")

    # Derived fields (year derived from event_date)
    if DERIVED_YEAR_FIELDS:
        lines.append("\nDerived Fields:")
        for field, source_field in DERIVED_YEAR_FIELDS.items():
            desc = FIELD_DESCRIPTIONS.get(field, f"Extracted from {source_field}")
            # Get date range from source field to show year range
            range_info = metadata.get_date_range(source_field)
            if range_info and range_info.min:
                try:
                    min_year = range_info.min[:4]
                    max_year = range_info.max[:4]
                    range_str = f"range [{min_year}, {max_year}]"
                except:
                    range_str = "integer"
            else:
                range_str = "integer"
            lines.append(f"  {field}: {desc}")
            lines.append(f"    - {range_str} (derived from {source_field})")

    # Date fields with descriptions and ranges
    lines.append("\nDate Fields:")
    for field in DATE_FIELDS:
        desc = FIELD_DESCRIPTIONS.get(field, "")
        range_info = metadata.get_date_range(field)
        if range_info and range_info.min:
            range_str = f"range [{range_info.min}, {range_info.max}]"
        else:
            range_str = "date field"
        if desc:
            lines.append(f"  {field}: {desc}")
            lines.append(f"    - {range_str}")
        else:
            lines.append(f"  {field}: {range_str}")

    # Unique ID field info
    lines.append(f"\nUnique ID field: {UNIQUE_ID_FIELD}")
    lines.append(f"Total unique IDs in index: {metadata.total_unique_ids}")

    return '\n'.join(lines)


def get_enhanced_docstring() -> str:
    """
    Get the tool docstring with dynamic field context injected.
    Call this after server startup when metadata is loaded.
    """
    field_context = build_dynamic_field_context()
    return ANALYTICS_DOCSTRING.replace(
        '</fields>',
        f'</fields>\n\n<field_context>\n{field_context}\n</field_context>'
    )


def update_tool2_description():
    """
    Update this tool's description with dynamic field context.
    Call this after server startup when metadata is loaded.
    """
    import shared_state

    if shared_state.mcp is None:
        logger.warning("MCP not initialized - cannot update tool description")
        return

    enhanced = get_enhanced_docstring()

    tool_name = analyze_all_events.__name__
    tool = shared_state.mcp._tool_manager._tools.get(tool_name)
    if tool:
        tool.description = enhanced
        logger.info(f"Updated {tool_name} tool description with field context")


# Export tool function and docstring for registration by main server
TOOL2_DOCSTRING = ANALYTICS_DOCSTRING
