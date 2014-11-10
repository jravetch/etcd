## Members API

* [List members](#list-members)
* [Add a member](#add-a-member)
* [Delete a member](#delete-a-member)

## List members

Return an HTTP 200 OK response code and a representation of all members in the etcd cluster.

### Request

```
GET /v2/members HTTP/1.1
```

### Example

```
curl http://10.0.0.10:2379/v2/members
```

```json
{
    "members": [
        {
            "id": "272e204152",
            "name": "infra1",
            "peerURLs": [
                "http://10.0.0.10:2380"
            ],
            "clientURLs": [
                "http://10.0.0.10:2379"
            ]
        },
        {
            "id": "2225373f43",
            "name": "infra2",
            "peerURLs": [
                "http://10.0.0.11:2380"
            ],
            "clientURLs": [
                "http://10.0.0.11:2379"
            ]
        },
    ]
}
```

## Add a member

Returns an HTTP 201 response code and the representation of added member with a newly generated a memberID when successful. Returns a string describing the failure condition when unsuccessful. 

If the POST body is malformed an HTTP 400 will be returned. If the member exists in the cluster or existed in the cluster at some point in the past an HTTP 409 will be returned. If any of the given peerURLs exists in the cluster an HTTP 409 will be returned. If the cluster fails to process the request within timeout an HTTP 500 will be returned, though the request may be processed later.

### Request

```
POST /v2/members HTTP/1.1

{"peerURLs": ["http://10.0.0.10:2379"]}
```

### Example

```
curl http://10.0.0.10:2379/v2/members -XPOST -H "Content-Type: application/json" -d '{"peerURLs":["http://10.0.0.10:2379"]}'
```

```json
{
    "id": "3777296169",
    "peerURLs": [
        "http://10.0.0.10:2379"
    ]
}
```

## Delete a member

Remove a member from the cluster. The member ID must be a hex-encoded uint64.
Returns empty when successful. Returns a string describing the failure condition when unsuccessful. 

If the member does not exist in the cluster an HTTP 500(TODO: fix this) will be returned. If the cluster fails to process the request within timeout an HTTP 500 will be returned, though the request may be processed later.

### Request

```
DELETE /v2/members/<id> HTTP/1.1
```

### Example

```
curl http://10.0.0.10:2379/v2/members/272e204152 -XDELETE
```
