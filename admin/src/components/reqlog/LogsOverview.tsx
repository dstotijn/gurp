import { useRouter } from "next/router";
import { gql, useQuery } from "@apollo/client";
import { useState } from "react";
import { Box, Typography, CircularProgress } from "@material-ui/core";
import Alert from "@material-ui/lab/Alert";

import RequestList from "./RequestList";
import LogDetail from "./LogDetail";
import CenteredPaper from "../CenteredPaper";

const HTTP_REQUEST_LOGS = gql`
  query HttpRequestLogs($filter: String!  = "") {
    httpRequestLogs(filter: $filter) {
      id
      method
      url
      timestamp
      response {
        status
        statusCode
      }
    }
  }
`;

function LogsOverview(): JSX.Element {
  const router = useRouter();
  const detailReqLogId = router.query.id as string;
  const filter = router.query.s as string;
  console.log(detailReqLogId);

  const { loading, error, data } = useQuery(HTTP_REQUEST_LOGS, { 
    variables: { filter },
    //pollInterval: 1000,
  });

  const handleLogClick = (reqId: string) => {
    var querystring ='?';
    if(filter != '')
    {
      querystring += 's='+filter+'&id='+reqId; 
    }
    else 
    {
      querystring += 'id='+reqId;
    }
    router.push("/proxy/logs" + querystring, undefined, {
      shallow: false,
    });
  };

  const handleSearch = (filter: string) => {
    router.push("/proxy/logs?s=" + filter, undefined, {
      shallow: false,
    });
  };

  if (loading) {
    return <CircularProgress />;
  }
  if (error) {
    return <Alert severity="error">Error fetching logs: {error.message}</Alert>;
  }

  const { httpRequestLogs: logs } = data;

  return (
    <div>
      <Box mb={2}>
        <RequestList
          logs={logs}
          selectedReqLogId={detailReqLogId}
          onLogClick={handleLogClick}
          onSearch={handleSearch}
        />
      </Box>
      <Box>
        {detailReqLogId && <LogDetail requestId={detailReqLogId} />}
        {logs.length !== 0 && !detailReqLogId && (
          <CenteredPaper>
            <Typography>Select a log entry…</Typography>
          </CenteredPaper>
        )}
      </Box>
    </div>
  );
}

export default LogsOverview;
