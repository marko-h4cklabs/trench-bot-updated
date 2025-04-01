# API Documentation

This document provides details about the available API endpoints and curl commands to test them.

---

## Base URL
All endpoints are prefixed with:
```
http://localhost:8080/api/v1
```

---

## Endpoints

### 1. Health Check
**Description**: Checks the health status of the API.

**Endpoint**:
```
GET /health
```

**Curl Command**:
```bash
curl -X GET http://localhost:8080/api/v1/health
```

**Expected Response**:
```json
{
  "message": "OK"
}
```

---

### 2. Analyse
**Description**: Endpoint to analyse input data.

**Endpoint**:
```
POST /analyse
```

**Curl Command**:
```bash
curl -X POST http://localhost:8080/api/v1/analyse \
  -H "Content-Type: application/json" \
  -d '{"key": "value"}'
```

**Expected Response**:
```json
{
  "message": "OK"
}
```

---

### 3. Listen
**Description**: Endpoint to listen for input events.

**Endpoint**:
```
POST /listen
```

**Curl Command**:
```bash
curl -X POST http://localhost:8080/api/v1/listen \
  -H "Content-Type: application/json" \
  -d '{"key": "value"}'
```

**Expected Response**:
```json
{
  "message": "OK"
}
```

---

### 4. Filter
**Description**: Endpoint to apply filters on input data.

**Endpoint**:
```
POST /filter
```

**Curl Command**:
```bash
curl -X POST http://localhost:8080/api/v1/filter \
  -H "Content-Type: application/json" \
  -d '{"filter": "criteria"}'
```

**Expected Response**:
```json
{
  "message": "OK"
}
```

---

## Notes
- All requests assume the server is running locally on port `8080`.
- Adjust the `-d` parameter in the curl commands with the appropriate payload for testing.

