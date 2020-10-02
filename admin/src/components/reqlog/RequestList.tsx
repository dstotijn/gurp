import {
  TableContainer,
  Paper,
  Grid,
  Table,
  TableHead,
  TableRow,
  TextField,
  TableCell,
  TableBody,
  Typography,
  Button,
  Box,
  createStyles,
  makeStyles,
  Theme,
  withTheme,
} from "@material-ui/core";

import HttpStatusIcon from "./HttpStatusCode";
import CenteredPaper from "../CenteredPaper";
import Search from "@material-ui/icons/List";
import { useRouter } from "next/router";
const useStyles = makeStyles((theme: Theme) =>
  createStyles({
    row: {
      "&:hover": {
        cursor: "pointer",
      },
    },
    /* Pseudo-class applied to the root element if `hover={true}`. */
    hover: {},
  })
);

interface Props {
  logs: Array<any>;
  selectedReqLogId?: string;
  onLogClick(requestId: string): void;
  onSearch(filter: string): void;
  theme: Theme;
}

function RequestList({
  logs,
  onLogClick,
  onSearch,
  selectedReqLogId,
  theme,
}: Props): JSX.Element {
  return (
    <div>
      <RequestListTable
        onLogClick={onLogClick}
        onSearch={onSearch}
        logs={logs}
        selectedReqLogId={selectedReqLogId}
        theme={theme}
      />
      {logs.length === 0 && (
        <Box my={1}>
          <CenteredPaper>
            <Typography>No logs found.</Typography>
          </CenteredPaper>
        </Box>
      )}
    </div>
  );
}

interface RequestListTableProps {
  logs?: any;
  selectedReqLogId?: string;
  onLogClick(requestId: string): void;
  onSearch(filter: string): void;
  theme: Theme;
}

function RequestListTable({
  logs,
  selectedReqLogId,
  onLogClick,
  onSearch,
  theme,
}: RequestListTableProps): JSX.Element {
  const classes = useStyles();
  const router = useRouter();
  var searchValue = '';
  const filter = router.query.s as string;
  const x = function (searchterm) {
    searchValue = searchterm;
  };
  return (
    <Box>
      <Grid
        container
        direction="row"
        justify="flex-start"
        alignItems="center"
      >
        <Grid item xs={11}>
          <TextField   style = {{width: '100%'}} id="searchbox" label="Search Term" variant="filled" size="small" onChange={e => x(e.target.value)} defaultValue={filter} />
        </Grid>
        <Grid item xs={1}>
          <Button
            variant="contained"
            color="secondary"
            onClick={e => onSearch(searchValue)}
            size="medium"
            startIcon={<Search />}
          >
            Search
        </Button>
        </Grid>
      </Grid>
      <TableContainer
        component={Paper}
        style={{
          minHeight: logs.length ? 200 : 0,
          height: logs.length ? "24vh" : "inherit",
        }}
      >
        <Table stickyHeader size="small">
          <TableHead>
            <TableRow>
              <TableCell>Method</TableCell>
              <TableCell>Origin</TableCell>
              <TableCell>Path</TableCell>
              <TableCell>Status</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {logs.map(({ id, method, url, response }) => {
              const { origin, pathname, search, hash } = new URL(url);

              const cellStyle = {
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              } as any;

              const rowStyle = {
                backgroundColor:
                  id === selectedReqLogId && theme.palette.action.selected,
              };

              return (
                <TableRow
                  key={id}
                  className={classes.row}
                  style={rowStyle}
                  hover
                  onClick={() => onLogClick(id)}
                >
                  <TableCell style={{ ...cellStyle, width: "100px" }}>
                    <code>{method}</code>
                  </TableCell>
                  <TableCell style={{ ...cellStyle, maxWidth: "100px" }}>
                    {origin}
                  </TableCell>
                  <TableCell style={{ ...cellStyle, maxWidth: "200px" }}>
                    {decodeURIComponent(pathname + search + hash)}
                  </TableCell>
                  <TableCell style={{ maxWidth: "100px" }}>
                    {response && (
                      <div>
                        <HttpStatusIcon status={response.statusCode} />{" "}
                        <code>{response.status}</code>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  );
}

export default withTheme(RequestList);
